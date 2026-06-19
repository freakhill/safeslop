// Package launch spawns a terminal window running an agent, with the ctty handoff intact.
// Adapters turn a shell command into the argv that opens it in the user's preferred terminal;
// the safeslop binary launched inside that terminal does the real `safeslop run` ctty handoff.
package launch

import "strings"

// appleScriptEscaper escapes a value for embedding inside an AppleScript double-quoted string
// literal: backslash first, then double-quote (a single left-to-right pass, no double-processing).
// Without this, a `"` in the command would break out of the literal and inject AppleScript.
var appleScriptEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`)

// taggingEnv returns the recognizability env injected into the child: the two SAFESLOP_* vars a
// user (or a WM rule / shell prompt) can key on.
func taggingEnv(session, cwd string) []string {
	return []string{"SAFESLOP_SESSION=" + session, "SAFESLOP_CWD=" + cwd}
}

// shellQuote single-quotes s for a POSIX shell ('\” escapes an embedded quote).
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Command assembles the shell command the terminal's shell runs: an optional OSC window title,
// a cd into the workspace, the SAFESLOP_* tagging env, then `exec <safeslop> run <profile>`. Baking the
// env into the command (rather than process env) is the only portable way to reach the new
// terminal window's shell across adapters.
func Command(safeslopPath, profile, cwd string, oscTitle bool) string {
	var b strings.Builder
	if oscTitle {
		// printf interprets the \033/\007 escapes (literal, single-quoted); the profile is
		// quoted separately so a metacharacter in it can't break out of the title.
		b.WriteString("printf '\\033]0;safeslop:'" + shellQuote(profile) + "'\\007'; ")
	}
	b.WriteString("cd " + shellQuote(cwd) + "; ")
	for _, kv := range taggingEnv(profile, cwd) {
		k, v, _ := strings.Cut(kv, "=")
		b.WriteString(k + "=" + shellQuote(v) + " ")
	}
	b.WriteString("exec " + shellQuote(safeslopPath) + " run " + shellQuote(profile))
	return b.String()
}

// AdapterArgv builds the argv that opens `command` (a POSIX-sh command line, e.g. from
// Command) in the named terminal, run through `shell` (default /bin/sh).
//
//   - Ghostty: `open -na Ghostty --args -e <shell> -lc <command>` — Ghostty's -e takes a
//     program + args, so the command is wrapped in a shell (a bare command string would be
//     exec'd as if it were a binary). Honors the user's preferred shell.
//   - iTerm2: AppleScript `create window with default profile command "<command>"` (the
//     command runs in a new iTerm window's own login shell).
//   - WezTerm: `open -na WezTerm --args start -- <shell> -lc <command>` (its `start`
//     subcommand spawns a window; `--` ends wezterm options so the shell args pass through).
//   - kitty: `open -na kitty --args <shell> -lc <command>` (kitty runs the first positional
//     as the program).
//   - Terminal.app (and "generic"/unknown — the always-present fallback): AppleScript
//     `do script`, which runs the command in a new Terminal window's own login shell.
func AdapterArgv(terminal, shell, command string) []string {
	if shell == "" {
		shell = "/bin/sh"
	}
	switch terminal {
	case "Ghostty":
		return []string{"open", "-na", "Ghostty", "--args", "-e", shell, "-lc", command}
	case "WezTerm":
		return []string{"open", "-na", "WezTerm", "--args", "start", "--", shell, "-lc", command}
	case "kitty":
		return []string{"open", "-na", "kitty", "--args", shell, "-lc", command}
	case "iTerm2":
		script := `tell application "iTerm" to create window with default profile command "` + appleScriptEscaper.Replace(command) + `"`
		return []string{"osascript", "-e", script}
	default: // "Terminal.app", "generic", any unknown value
		script := `tell application "Terminal" to do script "` + appleScriptEscaper.Replace(command) + `"`
		return []string{"osascript", "-e", script}
	}
}
