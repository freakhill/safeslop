// Package launch spawns a terminal window running an agent, with the ctty handoff intact.
// Adapters turn a shell command into the argv that opens it in the user's preferred terminal;
// the slop binary launched inside that terminal does the real `slop run` ctty handoff.
package launch

import "strings"

// appleScriptEscaper escapes a value for embedding inside an AppleScript double-quoted string
// literal: backslash first, then double-quote (a single left-to-right pass, no double-processing).
// Without this, a `"` in the command would break out of the literal and inject AppleScript.
var appleScriptEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`)

// taggingEnv returns the recognizability env injected into the child: the two SLOP_* vars a
// user (or a WM rule / shell prompt) can key on.
func taggingEnv(session, cwd string) []string {
	return []string{"SLOP_SESSION=" + session, "SLOP_CWD=" + cwd}
}

// shellQuote single-quotes s for a POSIX shell ('\” escapes an embedded quote).
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Command assembles the shell command the terminal's shell runs: an optional OSC window title,
// a cd into the workspace, the SLOP_* tagging env, then `exec <slop> run <profile>`. Baking the
// env into the command (rather than process env) is the only portable way to reach the new
// terminal window's shell across adapters.
func Command(slopPath, profile, cwd string, oscTitle bool) string {
	var b strings.Builder
	if oscTitle {
		b.WriteString(`printf '\033]0;slop:` + profile + `\007'; `)
	}
	b.WriteString("cd " + shellQuote(cwd) + "; ")
	b.WriteString(strings.Join(taggingEnv(profile, cwd), " ") + " ")
	b.WriteString("exec " + shellQuote(slopPath) + " run " + shellQuote(profile))
	return b.String()
}

// AdapterArgv builds the argv that opens `command` in the named terminal. Unknown terminals
// fall back to the generic `open -a` adapter.
func AdapterArgv(terminal, command, session string) []string {
	_ = session
	switch terminal {
	case "Terminal.app":
		script := `tell application "Terminal" to do script "` + appleScriptEscaper.Replace(command) + `"`
		return []string{"osascript", "-e", script}
	case "Ghostty":
		return []string{"open", "-na", "Ghostty", "--args", "-e", command}
	default: // "generic" and any unknown value
		return []string{"open", "-a", "Terminal", "--args", command}
	}
}
