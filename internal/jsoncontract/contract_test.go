package jsoncontract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestAllErrorCodesAreTheAppendOnlyV1Set(t *testing.T) {
	want := []ErrorCode{
		CodeInvalidArgument,
		CodeSchemaUnsupported,
		CodeSchemaViolation,
		CodeNotFound,
		CodeConflict,
		CodePermissionDenied,
		CodeAuthRequired,
		CodeCredentialRevoked,
		CodeCredentialRevokeFailed,
		CodePolicyDenied,
		CodeNetworkDenied,
		CodeSandboxDenied,
		CodeSandboxUnavailable,
		CodeRuntimeUnavailable,
		CodeToolUnavailable,
		CodeAgentUnsupported,
		CodeSessionNotFound,
		CodeSessionAlreadyRunning,
		CodeSessionStopped,
		CodeSessionCancelled,
		CodePTYUnavailable,
		CodeTimeout,
		CodeRateLimited,
		CodeIOError,
		CodeInternal,
	}
	got := AllErrorCodes()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AllErrorCodes() = %#v, want %#v", got, want)
	}
	seen := map[ErrorCode]bool{}
	for _, code := range got {
		if code == "" {
			t.Fatal("empty error code")
		}
		if seen[code] {
			t.Fatalf("duplicate error code %q", code)
		}
		seen[code] = true
		if !IsValidCode(code) {
			t.Fatalf("IsValidCode(%q) = false", code)
		}
	}
	if IsValidCode("NOT_A_CODE") {
		t.Fatal("IsValidCode accepted unknown code")
	}
}

func TestGoldenFixturesParseValidateAndRoundTrip(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("testdata", "*.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 8 {
		t.Fatalf("golden fixture count = %d, want 8: %v", len(paths), paths)
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			env, err := Unmarshal(b)
			if err != nil {
				t.Fatalf("Unmarshal(%s): %v", path, err)
			}
			if env.SchemaVersion != SchemaVersion {
				t.Fatalf("schema_version = %d, want %d", env.SchemaVersion, SchemaVersion)
			}
			if strings.HasPrefix(filepath.Base(path), "ok-") && !env.OK {
				t.Fatalf("%s ok = false, want true", path)
			}
			if strings.HasPrefix(filepath.Base(path), "error-") && env.OK {
				t.Fatalf("%s ok = true, want false", path)
			}
			marshaled, err := Marshal(env)
			if err != nil {
				t.Fatalf("Marshal(%s): %v", path, err)
			}
			if string(marshaled) != string(b) {
				t.Fatalf("%s is not canonical; Marshal(Unmarshal(fixture)) mismatch\n--- got ---\n%s\n--- want ---\n%s", path, marshaled, b)
			}
		})
	}
}

func TestPTYUnavailableEnvelopeMatchesGolden(t *testing.T) {
	want, err := os.ReadFile(filepath.Join("testdata", "error-pty-unavailable.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := Marshal(PTYUnavailable())
	if err != nil {
		t.Fatalf("Marshal(PTYUnavailable()): %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("PTYUnavailable() is not the golden wire shape\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestConstructorsProduceNonNilArraysAndObjects(t *testing.T) {
	ok := OK(nil)
	if err := Validate(ok); err != nil {
		t.Fatalf("Validate(OK(nil)): %v", err)
	}
	if ok.Data == nil || ok.Warnings == nil || ok.Errors == nil {
		t.Fatalf("OK(nil) left nil fields: %#v", ok)
	}
	if b, err := Marshal(ok); err != nil {
		t.Fatal(err)
	} else if strings.Contains(string(b), "null") {
		t.Fatalf("Marshal(OK(nil)) contains null: %s", b)
	}

	msg := NewMessage(CodeInvalidArgument, "bad argument", false, nil)
	env := Error(msg)
	if err := Validate(env); err != nil {
		t.Fatalf("Validate(Error(msg)): %v", err)
	}
	if env.Data == nil || env.Warnings == nil || env.Errors == nil || env.Errors[0].Details == nil {
		t.Fatalf("Error(msg) left nil fields: %#v", env)
	}
}

func TestValidateRejectsContractViolations(t *testing.T) {
	cases := []struct {
		name string
		env  Envelope
		want string
	}{
		{
			name: "schema unsupported",
			env:  Envelope{SchemaVersion: 2, OK: true, Data: map[string]any{}, Warnings: []Message{}, Errors: []Message{}},
			want: "unsupported schema_version",
		},
		{
			name: "nil data",
			env:  Envelope{SchemaVersion: 1, OK: true, Warnings: []Message{}, Errors: []Message{}},
			want: "data must be an object",
		},
		{
			name: "ok with errors",
			env:  Envelope{SchemaVersion: 1, OK: true, Data: map[string]any{}, Warnings: []Message{}, Errors: []Message{NewMessage(CodeInternal, "boom", false, nil)}},
			want: "ok envelope must not include errors",
		},
		{
			name: "error without errors",
			env:  Envelope{SchemaVersion: 1, OK: false, Data: map[string]any{}, Warnings: []Message{}, Errors: []Message{}},
			want: "error envelope must include",
		},
		{
			name: "unknown code",
			env:  Error(NewMessage("BOGUS", "boom", false, nil)),
			want: "unknown code",
		},
		{
			name: "empty message",
			env:  Error(NewMessage(CodeInternal, "", false, nil)),
			want: "message must be non-empty",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.env)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestUnmarshalRejectsNullStableFields(t *testing.T) {
	var raw map[string]any
	b, err := os.ReadFile(filepath.Join("testdata", "ok-minimal.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"data", "warnings", "errors"} {
		t.Run(field, func(t *testing.T) {
			clone := map[string]any{}
			for k, v := range raw {
				clone[k] = v
			}
			clone[field] = nil
			mutated, err := json.Marshal(clone)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Unmarshal(mutated); err == nil {
				t.Fatalf("Unmarshal accepted null %s", field)
			}
		})
	}
}
