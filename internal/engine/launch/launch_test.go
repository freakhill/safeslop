package launch

import (
	"strings"
	"testing"
)

func TestTaggingEnv(t *testing.T) {
	env := taggingEnv("review", "/work/repo")
	joined := strings.Join(env, " ")
	for _, want := range []string{"SLOP_SESSION=review", "SLOP_CWD=/work/repo"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("env missing %q: %v", want, env)
		}
	}
	if len(env) != 2 {
		t.Fatalf("env always carries exactly the 2 SLOP_* vars: %v", env)
	}
}

func TestGenericAdapterArgv(t *testing.T) {
	got := strings.Join(adapterArgv("generic", "/usr/local/bin/slop run review", "review"), " ")
	if !strings.Contains(got, "open -a") {
		t.Fatalf("generic adapter uses `open -a`: %q", got)
	}
}

func TestGhosttyAdapterArgv(t *testing.T) {
	got := strings.Join(adapterArgv("Ghostty", "slop run review", "review"), " ")
	if !strings.Contains(got, "Ghostty") || !strings.Contains(got, "slop run review") {
		t.Fatalf("ghostty adapter must open Ghostty running the command: %q", got)
	}
}

func TestTerminalAppAdapterUsesOsascript(t *testing.T) {
	got := strings.Join(adapterArgv("Terminal.app", "slop run review", "review"), " ")
	if !strings.HasPrefix(got, "osascript ") || !strings.Contains(got, "Terminal") {
		t.Fatalf("Terminal.app adapter drives osascript: %q", got)
	}
}

func TestUnknownAdapterFallsBackToGeneric(t *testing.T) {
	got := strings.Join(adapterArgv("Nope", "slop run review", "review"), " ")
	if !strings.Contains(got, "open -a") {
		t.Fatalf("unknown adapter falls back to generic open -a: %q", got)
	}
}

func TestTerminalAppAdapterEscapesInjection(t *testing.T) {
	// a command containing a double-quote / backslash must not break out of the AppleScript
	// string literal (command-injection regression).
	got := strings.Join(adapterArgv("Terminal.app", `slop run "x" & do shell script "rm -rf ~"`, "s"), " ")
	if !strings.Contains(got, `\"x\"`) {
		t.Fatalf("double-quotes must be backslash-escaped: %q", got)
	}
	// the closing literal quote of the wrapper must be the only unescaped `"` after the opener;
	// an injected `do script "..."` must appear escaped, not as live AppleScript.
	if strings.Contains(got, `do script "slop run "x"`) {
		t.Fatalf("unescaped quote broke out of the string literal: %q", got)
	}
}
