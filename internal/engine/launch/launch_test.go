package launch

import (
	"strings"
	"testing"
)

func TestTaggingEnv(t *testing.T) {
	env := taggingEnv("review", "/work/repo")
	joined := strings.Join(env, " ")
	for _, want := range []string{"SAFESLOP_SESSION=review", "SAFESLOP_CWD=/work/repo"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("env missing %q: %v", want, env)
		}
	}
	if len(env) != 2 {
		t.Fatalf("env always carries exactly the 2 SAFESLOP_* vars: %v", env)
	}
}

func TestGhosttyAdapterRunsCommandViaShell(t *testing.T) {
	// default shell (/bin/sh) when none configured; Ghostty's -e must get a program (the
	// shell), then -lc, then the command — NOT the bare command string.
	got := strings.Join(AdapterArgv("Ghostty", "", "safeslop run review"), " ")
	if got != "open -na Ghostty --args -e /bin/sh -lc safeslop run review" {
		t.Fatalf("ghostty argv = %q", got)
	}
	// honors the configured shell.
	withZsh := AdapterArgv("Ghostty", "/bin/zsh", "safeslop run review")
	if withZsh[5] != "/bin/zsh" || withZsh[6] != "-lc" || withZsh[7] != "safeslop run review" {
		t.Fatalf("ghostty must use the configured shell: %v", withZsh)
	}
}

func TestTerminalAppAdapterUsesOsascript(t *testing.T) {
	got := strings.Join(AdapterArgv("Terminal.app", "", "safeslop run review"), " ")
	if !strings.HasPrefix(got, "osascript ") || !strings.Contains(got, "Terminal") {
		t.Fatalf("Terminal.app adapter drives osascript: %q", got)
	}
}

func TestGenericAndUnknownFallBackToTerminal(t *testing.T) {
	for _, term := range []string{"generic", "Nope"} {
		got := strings.Join(AdapterArgv(term, "", "safeslop run review"), " ")
		if !strings.HasPrefix(got, "osascript ") || !strings.Contains(got, "Terminal") {
			t.Fatalf("%q must fall back to the Terminal.app osascript adapter: %q", term, got)
		}
	}
}

func TestTerminalAppAdapterEscapesInjection(t *testing.T) {
	// a command containing a double-quote / backslash must not break out of the AppleScript
	// string literal (command-injection regression).
	got := strings.Join(AdapterArgv("Terminal.app", "", `safeslop run "x" & do shell script "rm -rf ~"`), " ")
	if !strings.Contains(got, `\"x\"`) {
		t.Fatalf("double-quotes must be backslash-escaped: %q", got)
	}
	if strings.Contains(got, `do script "safeslop run "x"`) {
		t.Fatalf("unescaped quote broke out of the string literal: %q", got)
	}
}

func TestCommandBakesTaggingAndExec(t *testing.T) {
	cmd := Command("/usr/local/bin/safeslop", "review", "/work/repo", true)
	for _, want := range []string{
		`printf '\033]0;safeslop:''review'`,
		`cd '/work/repo'`,
		`SAFESLOP_SESSION='review' SAFESLOP_CWD='/work/repo'`,
		`exec '/usr/local/bin/safeslop' run 'review'`,
	} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("Command missing %q: %s", want, cmd)
		}
	}
	// oscTitle off => no printf title
	if strings.Contains(Command("/s", "r", "/w", false), "printf") {
		t.Fatalf("oscTitle=false must omit the title printf")
	}
}

func TestCommandQuotesAgainstInjection(t *testing.T) {
	// a cwd carrying shell metacharacters must be single-quoted (embedded quote escaped),
	// not interpolated raw — no command-injection via the workspace path.
	cmd := Command("/s", "p", `/tmp/x'; rm -rf ~ #`, true)
	if !strings.Contains(cmd, `'\''`) {
		t.Fatalf("cwd quote not escaped (injection vector): %s", cmd)
	}
}

func TestITerm2AdapterUsesOsascript(t *testing.T) {
	got := strings.Join(AdapterArgv("iTerm2", "", "safeslop run review"), " ")
	if !strings.HasPrefix(got, "osascript ") || !strings.Contains(got, `application "iTerm"`) || !strings.Contains(got, "safeslop run review") {
		t.Fatalf("iTerm2 adapter must drive osascript on iTerm: %q", got)
	}
}

func TestWezTermAdapterRunsCommandViaShell(t *testing.T) {
	got := strings.Join(AdapterArgv("WezTerm", "", "safeslop run review"), " ")
	if got != "open -na WezTerm --args start -- /bin/sh -lc safeslop run review" {
		t.Fatalf("wezterm argv = %q", got)
	}
}

func TestKittyAdapterRunsCommandViaShell(t *testing.T) {
	got := strings.Join(AdapterArgv("kitty", "/bin/zsh", "safeslop run review"), " ")
	if got != "open -na kitty --args /bin/zsh -lc safeslop run review" {
		t.Fatalf("kitty argv = %q", got)
	}
}
