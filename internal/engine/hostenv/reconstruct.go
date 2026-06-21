package hostenv

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// captureTimeout bounds the shell run: a misconfigured profile that blocks on ssh-add/gpg/nvm must
// never freeze the cockpit — we fall back to the deterministic floor instead.
const captureTimeout = 3 * time.Second

// Env is a reconstructed host environment: a name→value map plus its provenance. It is RICH (a login
// shell exports cloud tokens etc.) and is used ONLY for host-side discovery and binary resolution —
// see the package doc's firewall note. Methods are read-only.
type Env struct {
	vars   map[string]string
	Source string // "current" | "shell:<name>" | "fallback"

	// isExec reports whether path is an existing regular executable file (injected for tests).
	isExec func(string) bool
}

// PATH returns the reconstructed PATH (already sanitized).
func (e *Env) PATH() string { return e.vars["PATH"] }

// Get returns a reconstructed variable and whether it was present.
func (e *Env) Get(name string) (string, bool) { v, ok := e.vars[name]; return v, ok }

// Environ renders the env as a "K=V" slice (host_discovery_env for exec.Cmd.Env on the host side).
func (e *Env) Environ() []string {
	out := make([]string, 0, len(e.vars))
	for k, v := range e.vars {
		out = append(out, k+"="+v)
	}
	return out
}

// LookPath resolves file against the reconstructed PATH, like exec.LookPath but using this Env's PATH
// rather than the process PATH (which is stripped under Finder). A name containing a slash is checked
// directly. Returns the absolute path and true on success.
func (e *Env) LookPath(file string) (string, bool) {
	if strings.Contains(file, "/") {
		if e.isExec(file) {
			return file, true
		}
		return "", false
	}
	for _, dir := range strings.Split(e.PATH(), ":") {
		if dir == "" {
			continue
		}
		full := filepath.Join(dir, file)
		if e.isExec(full) {
			return full, true
		}
	}
	return "", false
}

// LookAll resolves file against EVERY directory in the reconstructed PATH, returning all existing
// executable matches in PATH order (like `which -a`). The first is the one LookPath returns (the winner);
// any others are shadowed — a later-PATH install the user may have meant. Flagging this matters because
// picking the wrong binary can silently differ from what the user expects. A slash-containing name is
// checked directly (zero or one result).
func (e *Env) LookAll(file string) []string {
	if strings.Contains(file, "/") {
		if e.isExec(file) {
			return []string{file}
		}
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, dir := range strings.Split(e.PATH(), ":") {
		if dir == "" {
			continue
		}
		full := filepath.Join(dir, file)
		if seen[full] || !e.isExec(full) {
			continue
		}
		out = append(out, full)
		seen[full] = true
	}
	return out
}

// reconstructor holds the (injectable) host seams plus the in-memory session cache. All exported
// entry points go through the package-level default; tests build one directly with fake seams.
type reconstructor struct {
	mu     sync.Mutex
	cached *Env
	key    string
	primed bool

	environ      func() []string
	homeDir      func() (string, error)
	username     func() string
	resolveShell func(user string) (string, error)
	newMarker    func() string
	runShell     func(shellPath string, argv []string) (string, error)
	pathHelper   func() string
	stat         statFunc
	mtimeKey     func(home string) string
	isExec       func(string) bool
}

// get returns the cached Env, recomputing when the env was never built or any tracked shell-config
// file changed (mtime key differs). The shell capture is the expensive part, so this is what keeps an
// Installs-tab click from paying a shell startup every time — while a mid-session brew install (which
// does not touch an rc file) still appears, because detection re-runs LookPath against the cached PATH.
func (r *reconstructor) get() *Env {
	r.mu.Lock()
	defer r.mu.Unlock()
	home, _ := r.homeDir()
	key := r.mtimeKey(home)
	if r.primed && r.cached != nil && key == r.key {
		return r.cached
	}
	env := r.compute(home)
	r.cached, r.key, r.primed = env, key, true
	return env
}

// compute builds a fresh Env: when the process already has a rich env (a terminal launch) it is used
// as-is; otherwise the shell is captured, and on any capture failure the deterministic floor is used.
func (r *reconstructor) compute(home string) *Env {
	environ := r.environ()
	if !isGUIMinimal(environ) {
		return r.newEnv(environToMap(environ), "current")
	}
	if vars, src, ok := r.capture(home); ok {
		// Even a successful capture gets the floor merged in (after the shell's own order) so a thin
		// PATH still resolves brew/user/shim dirs.
		vars["PATH"] = mergePATH(vars["PATH"], r.fallbackPATH(home))
		return r.newEnv(vars, src)
	}
	return r.newEnv(map[string]string{"PATH": r.fallbackPATH(home), "HOME": home}, "fallback")
}

// capture runs the user's login+interactive shell and reads the marker-wrapped env dump. It tolerates
// a non-nil run error as long as the markers are present (a shell that dumped the env and then hung
// past the timeout still yields a usable result); it fails only when the dump can't be parsed.
func (r *reconstructor) capture(home string) (map[string]string, string, bool) {
	shell, err := r.resolveShell(r.username())
	if err != nil || shell == "" {
		return nil, "", false
	}
	marker := r.newMarker()
	out, _ := r.runShell(shell, shellArgv(captureScript(marker)))
	vars, err := parseMarkerEnv(out, marker)
	if err != nil {
		return nil, "", false
	}
	vars = sanitize(vars, r.stat)
	if vars["PATH"] == "" {
		return nil, "", false
	}
	return vars, "shell:" + filepath.Base(shell), true
}

// fallbackPATH composes the deterministic floor: path_helper's /etc/paths output (runs no user code)
// plus the hardcoded brew/user/shim dirs, sanitized.
func (r *reconstructor) fallbackPATH(home string) string {
	var dirs []string
	if ph := r.pathHelper(); ph != "" {
		dirs = append(dirs, strings.Split(ph, ":")...)
	}
	dirs = append(dirs, hardcodedDirs(home)...)
	return filterPATH(strings.Join(dirs, ":"), r.stat)
}

func (r *reconstructor) newEnv(vars map[string]string, source string) *Env {
	check := r.isExec
	if check == nil {
		check = realIsExec
	}
	return &Env{vars: vars, Source: source, isExec: check}
}

// captureScript is the inner -c program: print marker, dump the process env via the ABSOLUTE external
// env (PATH-independent, and bypassing any shell builtin/function/fish abbreviation that shadows
// `env`), print marker. Defensive runtime knobs (TERM=dumb, stdin /dev/null, timeout) are set on the
// exec.Cmd by realRunShell, not here. The marker is alphanumeric+dash, so it needs no quoting.
func captureScript(marker string) string {
	return "printf '%s\\n' " + marker + "; /usr/bin/env; printf '%s\\n' " + marker
}

// mergePATH concatenates two PATHs, keeping primary's order first, appending only fallback entries not
// already present, and dropping empties/dupes.
func mergePATH(primary, fallback string) string {
	seen := map[string]bool{}
	var out []string
	for _, dir := range append(strings.Split(primary, ":"), strings.Split(fallback, ":")...) {
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		out = append(out, dir)
	}
	return strings.Join(out, ":")
}

// parsePathHelper extracts the PATH value from `/usr/libexec/path_helper -s` output, whose first line
// is `PATH="/usr/bin:/bin:..."; export PATH;`.
func parsePathHelper(s string) string {
	const pre = `PATH="`
	i := strings.Index(s, pre)
	if i < 0 {
		return ""
	}
	rest := s[i+len(pre):]
	j := strings.IndexByte(rest, '"')
	if j < 0 {
		return ""
	}
	return rest[:j]
}

func environToMap(environ []string) map[string]string {
	m := make(map[string]string, len(environ))
	for _, kv := range environ {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k] = v
		}
	}
	return m
}

// --- real host seams ---------------------------------------------------------------------------

func newDefaultReconstructor() *reconstructor {
	return &reconstructor{
		environ:      os.Environ,
		homeDir:      os.UserHomeDir,
		username:     realUsername,
		resolveShell: realResolveShell,
		newMarker:    realMarker,
		runShell:     realRunShell,
		pathHelper:   realPathHelper,
		stat:         realStat,
		mtimeKey:     realMtimeKey,
		isExec:       realIsExec,
	}
}

func realUsername() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return os.Getenv("USER")
}

// realResolveShell reads the login shell from Directory Services rather than $SHELL (absent under
// Finder). Output is `UserShell: /bin/zsh`.
func realResolveShell(username string) (string, error) {
	out, err := exec.Command("dscl", ".", "-read", "/Users/"+username, "UserShell").Output()
	if err != nil {
		return "", err
	}
	s := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(out)), "UserShell:"))
	return s, nil
}

func realMarker() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "SAFESLOP-HOSTENV-" + hex.EncodeToString(b[:])
}

// realRunShell runs the capture defensively: a hard timeout, stdin from /dev/null, and a prompt-free
// non-interactive-ish env so dotfiles don't block on a prompt hook or update check. stderr is
// discarded (MOTD/banner noise); only stdout between the markers matters.
func realRunShell(shellPath string, argv []string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), captureTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, shellPath, argv...)
	cmd.Stdin = nil // /dev/null
	cmd.Env = append(os.Environ(),
		"TERM=dumb", "CI=1", "NONINTERACTIVE=1",
		"PS1=", "PROMPT_COMMAND=", "PROMPT=",
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = nil
	err := cmd.Run()
	return out.String(), err
}

func realPathHelper() string {
	out, err := exec.Command("/usr/libexec/path_helper", "-s").Output()
	if err != nil {
		return ""
	}
	return parsePathHelper(string(out))
}

func realStat(p string) (fs.FileMode, error) {
	fi, err := os.Stat(p)
	if err != nil {
		return 0, err
	}
	return fi.Mode(), nil
}

func realIsExec(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0
}

// rcFiles are the shell-config files whose mtime invalidates the cache (a brew install does not touch
// these, so a mid-session install still surfaces via re-detection against the cached PATH).
func rcFiles(home string) []string {
	names := []string{
		".zshenv", ".zprofile", ".zshrc",
		".bash_profile", ".bashrc", ".profile",
		".config/fish/config.fish",
	}
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = filepath.Join(home, filepath.FromSlash(n))
	}
	return out
}

func realMtimeKey(home string) string {
	var b strings.Builder
	for _, f := range rcFiles(home) {
		if fi, err := os.Stat(f); err == nil {
			fmt.Fprintf(&b, "%s=%d;", f, fi.ModTime().UnixNano())
		}
	}
	return b.String()
}

// defaultR is the process-wide reconstructor; Reconstruct returns its (cached) Env.
var defaultR = newDefaultReconstructor()

// Reconstruct returns the host environment for discovery and binary resolution, rebuilding it only
// when a shell-config file changed. Safe for concurrent use.
func Reconstruct() *Env { return defaultR.get() }
