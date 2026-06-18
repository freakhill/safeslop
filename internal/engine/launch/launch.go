// Package launch spawns a terminal window running an agent, with the ctty handoff intact.
// Adapters turn a shell command into the argv that opens it in the user's preferred terminal.
package launch

import "strings"

// appleScriptEscaper escapes a value for embedding inside an AppleScript double-quoted string
// literal: backslash first, then double-quote (a single left-to-right pass, no double-processing).
// Without this, a `"` in the command would break out of the literal and inject AppleScript.
var appleScriptEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`)

// taggingEnv returns the recognizability env injected into the child: the two SLOP_* vars a
// user (or a WM rule / shell prompt) can key on. The OSC window title is emitted by the
// spawned shell wrapper, not here.
func taggingEnv(session, cwd string) []string {
	return []string{"SLOP_SESSION=" + session, "SLOP_CWD=" + cwd}
}

// adapterArgv builds the argv that opens `command` in the named terminal. Unknown terminals
// fall back to the generic `open -a` adapter.
func adapterArgv(terminal, command, session string) []string {
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
