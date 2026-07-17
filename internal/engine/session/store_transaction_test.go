package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreListRejectsCorruptRecordWithoutPartialResults(t *testing.T) {
	store := NewStore(t.TempDir())
	if _, err := store.Create("fish", "host", t.TempDir(), testNow()); err != nil {
		t.Fatalf("create valid session: %v", err)
	}
	if err := os.WriteFile(filepath.Join(store.Dir, "sess-corrupt.json"), []byte("{not-json\n"), 0o600); err != nil {
		t.Fatalf("write corrupt record: %v", err)
	}

	sessions, err := store.List()
	if err == nil {
		t.Fatalf("List returned %d partial sessions and no corruption error", len(sessions))
	}
	if sessions != nil {
		t.Fatalf("List returned partial sessions on corruption: %#v", sessions)
	}
}

func TestStoreRejectsStaleSaveInsteadOfOverwritingNewerMutation(t *testing.T) {
	storeA := NewStore(t.TempDir())
	created, err := storeA.Create("fish", "host", t.TempDir(), testNow())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	storeB := NewStore(storeA.Dir)

	first, err := storeA.Get(created.ID)
	if err != nil {
		t.Fatalf("first Get: %v", err)
	}
	stale, err := storeB.Get(created.ID)
	if err != nil {
		t.Fatalf("stale Get: %v", err)
	}
	first.Name = "kept-newer-name"
	if err := storeA.Save(first); err != nil {
		t.Fatalf("save first mutation: %v", err)
	}
	stale.EgressAcknowledgements = append(stale.EgressAcknowledgements, EgressAcknowledgement{
		Host: "api.example.com", Port: 443, AcknowledgedAt: testNow(),
	})
	if err := storeB.Save(stale); err == nil {
		t.Fatal("stale Save succeeded and could overwrite a newer independent mutation")
	}

	got, err := storeA.Get(created.ID)
	if err != nil {
		t.Fatalf("final Get: %v", err)
	}
	if got.Name != "kept-newer-name" {
		t.Fatalf("newer mutation was lost: Name = %q", got.Name)
	}
}

func TestStoreAtomicPreRenameFailureKeepsCompletePriorRecord(t *testing.T) {
	store := NewStore(t.TempDir())
	created, err := store.Create("fish", "host", t.TempDir(), testNow())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	candidate, err := store.Get(created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	candidate.Name = "must-not-appear"
	faulted := NewStore(store.Dir)
	faulted.hooks = &atomicHooks{syncFile: func(*os.File) error { return errors.New("injected sync failure") }}
	if err := faulted.Save(candidate); err == nil {
		t.Fatal("Save succeeded despite injected pre-rename sync failure")
	}
	got, err := store.Get(created.ID)
	if err != nil {
		t.Fatalf("Get after failed Save: %v", err)
	}
	if got.Name != "" {
		t.Fatalf("failed pre-rename commit changed durable record: Name = %q", got.Name)
	}
}

func TestStoreRenameFailureKeepsCompletePriorRecord(t *testing.T) {
	store := NewStore(t.TempDir())
	created, err := store.Create("fish", "host", t.TempDir(), testNow())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	candidate, err := store.Get(created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	candidate.Name = "must-not-appear"
	faulted := NewStore(store.Dir)
	faulted.hooks = &atomicHooks{rename: func(string, string) error { return errors.New("injected rename failure") }}
	if err := faulted.Save(candidate); err == nil {
		t.Fatal("Save succeeded despite injected rename failure")
	}
	got, err := store.Get(created.ID)
	if err != nil {
		t.Fatalf("Get after failed Save: %v", err)
	}
	if got.Name != "" {
		t.Fatalf("failed rename changed durable record: Name = %q", got.Name)
	}
}

func TestStoreFailedNoReplaceCreateLeavesNoListableRecord(t *testing.T) {
	store := NewStore(t.TempDir())
	store.hooks = &atomicHooks{link: func(string, string) error { return errors.New("injected link failure") }}
	candidate := Session{ID: "sess-create-fault", Agent: "fish", Workspace: "/tmp", Environment: "host", Network: "deny", Status: StatusCreated, CreatedAt: testNow(), UpdatedAt: testNow()}
	if err := store.Save(candidate); err == nil {
		t.Fatal("Save create succeeded despite injected no-replace failure")
	}
	listed, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 0 {
		t.Fatalf("failed create left a listable partial record: %#v", listed)
	}
}

func TestStoreDirectorySyncFailureReturnsUncertainWithCompleteRecord(t *testing.T) {
	store := NewStore(t.TempDir())
	created, err := store.Create("fish", "host", t.TempDir(), testNow())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	candidate, err := store.Get(created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	candidate.Name = "complete-new-record"
	faulted := NewStore(store.Dir)
	faulted.hooks = &atomicHooks{syncDir: func(string) error { return errors.New("injected directory sync failure") }}
	if err := faulted.Save(candidate); !errors.Is(err, ErrCommitUncertain) {
		t.Fatalf("Save error = %v, want ErrCommitUncertain", err)
	}
	got, err := store.Get(created.ID)
	if err != nil {
		t.Fatalf("Get after uncertain Save: %v", err)
	}
	if got.Name != "complete-new-record" {
		t.Fatalf("reader observed neither complete new record nor expected value: %#v", got)
	}
}

func TestStoreRejectsWrongIdentityStatusAndSymlinkAsCorruption(t *testing.T) {
	store := NewStore(t.TempDir())
	cases := map[string]string{
		"sess-wrong-id":   `{"session_id":"sess-other","status":"created"}`,
		"sess-bad-status": `{"session_id":"sess-bad-status","status":"mystery"}`,
		"sess-bad-layout": `{"session_id":"sess-bad-layout","status":"created","runtime_id":"sess-other","stage_layout":2}`,
	}
	for id, body := range cases {
		if err := os.WriteFile(filepath.Join(store.Dir, id+".json"), []byte(body+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Get(id); !errors.Is(err, ErrCorruptRecord) {
			t.Fatalf("Get(%s) error = %v, want ErrCorruptRecord", id, err)
		}
	}
	target := filepath.Join(store.Dir, "outside.json")
	if err := os.WriteFile(target, []byte(`{"session_id":"sess-link","status":"created"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(store.Dir, "sess-link.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("sess-link"); !errors.Is(err, ErrCorruptRecord) {
		t.Fatalf("Get symlink error = %v, want ErrCorruptRecord", err)
	}
}

func TestStoreCreateUsesInternalSessionIDStageLayout(t *testing.T) {
	store := NewStore(t.TempDir())
	created, err := store.Create("fish", "container", t.TempDir(), testNow())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if runtimeID, layout := created.RuntimeIdentity(); runtimeID != created.ID || layout != StageLayoutSessionID {
		t.Fatalf("RuntimeIdentity = (%q,%d), want (%q,%d)", runtimeID, layout, created.ID, StageLayoutSessionID)
	}
	onDisk, err := os.ReadFile(filepath.Join(store.Dir, created.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"runtime_id": "` + created.ID + `"`, `"stage_layout": 2`} {
		if !strings.Contains(string(onDisk), want) {
			t.Fatalf("new record missing %s: %s", want, onDisk)
		}
	}
	public, err := json.Marshal(created)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(public), "runtime_id") || strings.Contains(string(public), "stage_layout") {
		t.Fatalf("runtime routing leaked into Session JSON: %s", public)
	}
}

func TestStoreLegacyRecordGainsRevisionOnlyOnMutation(t *testing.T) {
	store := NewStore(t.TempDir())
	legacy := `{"session_id":"sess-legacy","agent":"fish","workspace":"/tmp","environment":"host","network":"deny","backend":"","status":"created","created_at":"2026-06-26T00:00:00Z","updated_at":"2026-06-26T00:00:00Z","credentials_revoked":false}`
	if err := os.WriteFile(filepath.Join(store.Dir, "sess-legacy.json"), []byte(legacy+"\n"), 0o600); err != nil {
		t.Fatalf("write legacy record: %v", err)
	}
	got, err := store.Get("sess-legacy")
	if err != nil {
		t.Fatalf("Get legacy: %v", err)
	}
	if runtimeID, layout := got.RuntimeIdentity(); runtimeID != "" || layout != StageLayoutLegacy {
		t.Fatalf("legacy RuntimeIdentity = (%q,%d), want empty legacy layout", runtimeID, layout)
	}
	before, err := os.ReadFile(filepath.Join(store.Dir, "sess-legacy.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(before), "record_revision") {
		t.Fatalf("read rewrote legacy record: %s", before)
	}
	got.Name = "mutated"
	if err := store.Save(got); err != nil {
		t.Fatalf("Save legacy mutation: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(store.Dir, "sess-legacy.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), `"record_revision": 1`) {
		t.Fatalf("legacy mutation did not add revision 1: %s", after)
	}
	if strings.Contains(string(after), "runtime_id") || strings.Contains(string(after), "stage_layout") {
		t.Fatalf("legacy mutation silently changed stage layout: %s", after)
	}
}

func TestStoreConcurrentProcessesPreserveIndependentUpdates(t *testing.T) {
	store := NewStore(t.TempDir())
	created, err := store.Create("fish", "host", t.TempDir(), testNow())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	commands := []*exec.Cmd{
		exec.Command(os.Args[0], "-test.run=^TestStoreUpdateHelperProcess$"),
		exec.Command(os.Args[0], "-test.run=^TestStoreUpdateHelperProcess$"),
	}
	commands[0].Env = append(os.Environ(), "SAFESLOP_STORE_HELPER=1", "SAFESLOP_STORE_DIR="+store.Dir, "SAFESLOP_STORE_ID="+created.ID, "SAFESLOP_STORE_CHANGE=name")
	commands[1].Env = append(os.Environ(), "SAFESLOP_STORE_HELPER=1", "SAFESLOP_STORE_DIR="+store.Dir, "SAFESLOP_STORE_ID="+created.ID, "SAFESLOP_STORE_CHANGE=ack")
	outputs := make([]bytes.Buffer, len(commands))
	for i, cmd := range commands {
		cmd.Stdout, cmd.Stderr = &outputs[i], &outputs[i]
		if err := cmd.Start(); err != nil {
			t.Fatalf("start helper: %v", err)
		}
	}
	for i, cmd := range commands {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("helper failed: %v\n%s", err, outputs[i].Bytes())
		}
	}
	got, err := store.Get(created.ID)
	if err != nil {
		t.Fatalf("Get final: %v", err)
	}
	if got.Name != "concurrent-name" || len(got.EgressAcknowledgements) != 1 {
		t.Fatalf("concurrent updates lost a successful mutation: %#v", got)
	}
}

func TestStoreUpdateHelperProcess(t *testing.T) {
	if os.Getenv("SAFESLOP_STORE_HELPER") != "1" {
		t.Skip("subprocess helper")
	}
	store := NewStore(os.Getenv("SAFESLOP_STORE_DIR"))
	id := os.Getenv("SAFESLOP_STORE_ID")
	_, err := store.Update(id, func(sess Session) (Session, error) {
		switch os.Getenv("SAFESLOP_STORE_CHANGE") {
		case "name":
			sess.Name = "concurrent-name"
		case "ack":
			sess.EgressAcknowledgements = append(sess.EgressAcknowledgements, EgressAcknowledgement{Host: "api.example.com", Port: 443, AcknowledgedAt: testNow()})
		default:
			return Session{}, fmt.Errorf("unknown helper change")
		}
		return sess, nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
}

func TestStoreRevisionStaysInternalToSessionJSON(t *testing.T) {
	store := NewStore(t.TempDir())
	created, err := store.Create("fish", "host", t.TempDir(), testNow())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.Rename(created.ID, "one", testNow()); err != nil {
		t.Fatalf("rename: %v", err)
	}
	got, err := store.Get(created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	public, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal public session: %v", err)
	}
	if strings.Contains(string(public), "record_revision") {
		t.Fatalf("internal revision leaked into Session JSON: %s", public)
	}
}

func TestStoreErrorsAreTypedAndValueFree(t *testing.T) {
	for _, sentinel := range []error{ErrCorruptRecord, ErrStaleRecord, ErrCommitUncertain} {
		if !errors.Is(sentinel, sentinel) {
			t.Fatalf("sentinel is not errors.Is compatible: %v", sentinel)
		}
		text := sentinel.Error()
		if strings.Contains(text, "/") || strings.Contains(text, "sess-") {
			t.Fatalf("store sentinel contains record values: %q", text)
		}
	}
}
