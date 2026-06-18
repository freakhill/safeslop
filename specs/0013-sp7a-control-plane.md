# SP7a — control plane + data plane + terminal-launch Implementation Plan

**Goal:** Build the engine side of the SP7 GUI (`specs/0012`): a gRPC-over-Unix-domain-socket control plane (`slop serve`), the `slop launch <profile>` data-plane trigger (terminal spawn, ctty intact), and the `~/.config/slop/config.cue` terminal-launch subsystem.

**Architecture:** A `.proto` contract (`internal/engine/control/control.proto`) generates Go server stubs (committed; `make proto`). `slop serve` binds `~/.slop/s.sock` (0700 dir / 0600 socket), enforces same-uid peer-auth (`LOCAL_PEERCRED`, CGO-free), and serves a `Control` service (`Ping`, `ListProfiles`, `Launch`-streaming). `slop launch` and the `Launch` RPC both call one launch core that reads `~/.config/slop/config.cue` (a new `userconfig` package), picks a terminal adapter (Terminal.app / Ghostty / generic-fallback), and spawns a terminal running the agent via the existing ctty path. Pure-Go testable cores (argv builders, config parse, uid check via socketpair) + guarded integration tests for the real serve/spawn.

**Tech stack:** Go, `google.golang.org/grpc` (new dep) + `google.golang.org/protobuf`, `golang.org/x/sys/unix` (LOCAL_PEERCRED), embedded CUE (`cuelang.org/go`), cobra. protoc toolchain at dev time only (generated code committed).

**Base branch:** `sp7a-control-plane` off `main` (already holds `specs/0012`).

**File structure:**
- `internal/engine/control/control.proto` (create) — the gRPC contract.
- `internal/engine/control/pb/*.pb.go` (generated, committed) — Go message + server/client stubs.
- `internal/engine/control/server.go` (create) — `Control` service impl (delegates Launch to the launch core).
- `internal/engine/control/peerauth.go` (create) — `LOCAL_PEERCRED` same-uid `credentials.TransportCredentials`.
- `internal/engine/control/peerauth_test.go` (create) — socketpair uid-check test.
- `internal/engine/control/serve.go` (create) — UDS bind (short path, perms) + gRPC serve loop.
- `internal/engine/userconfig/userconfig.go` (create) — `~/.config/slop/config.cue` schema + Load.
- `internal/engine/userconfig/schema/config.cue` (create) — embedded CUE schema.
- `internal/engine/userconfig/userconfig_test.go` (create) — parse + defaults tests.
- `internal/engine/launch/launch.go` (create) — terminal adapters (argv + tagging) + launch core.
- `internal/engine/launch/launch_test.go` (create) — adapter argv + tagging-env tests.
- `internal/cli/cli.go` (modify) — `slop serve` + `slop launch` cobra commands.
- `internal/cli/cli_test.go` (modify) — command wiring smoke tests.
- `Makefile` (modify) — `proto` target.
- `go.mod`/`go.sum` (modify) — add grpc.

---

## Design decisions (from specs/0012 §9; vetoable)

1. Peer-auth v1 = **uid-only** (CGO-free); codesign-identity deferred. 2. **gRPC** as locked. 3. `userconfig` is a **new package** (user-level, not policy). 4. Adapters v1 = **Terminal.app + Ghostty + generic-fallback**; iTerm2-native + kitty/wezterm later. 5. `make proto` requires protoc locally; **generated code committed** so CI/`make build` never needs protoc.

---

### Task 1: the `.proto` contract + generated stubs + `make proto`

**Files:**
- Create: `internal/engine/control/control.proto`
- Generated: `internal/engine/control/pb/control.pb.go`, `control_grpc.pb.go`
- Modify: `Makefile`, `go.mod`
- Test: `internal/engine/control/pb/pb_smoke_test.go`

- [ ] **Step 1: Write the contract.** Create `internal/engine/control/control.proto`:

```proto
syntax = "proto3";
package slop.control.v1;
option go_package = "github.com/freakhill/safeslop/internal/engine/control/pb;pb";

// Control is the app<->engine control plane (gRPC over a Unix-domain socket).
service Control {
  rpc Ping(PingRequest) returns (PingResponse);
  rpc ListProfiles(ListProfilesRequest) returns (ListProfilesResponse);
  rpc Launch(LaunchRequest) returns (stream LaunchEvent);
}

message PingRequest {}
message PingResponse { string version = 1; }

message ListProfilesRequest { string config_path = 1; } // dir holding slop.cue; empty = server cwd
message Profile {
  string name = 1;
  string agent = 2;
  string environment = 3;
  string network = 4;
}
message ListProfilesResponse { repeated Profile profiles = 1; }

message LaunchRequest {
  string profile = 1;
  string config_path = 2;
}
message LaunchEvent {
  enum Kind {
    SPAWNED = 0;
    EXITED = 1;
    ERROR = 2;
  }
  Kind kind = 1;
  string message = 2;
  int32 exit_code = 3;
}
```

- [ ] **Step 2: Add the `proto` make target.** In `Makefile`, add:

```make
.PHONY: proto
proto:
	protoc --go_out=. --go_opt=module=github.com/freakhill/safeslop \
	       --go-grpc_out=. --go-grpc_opt=module=github.com/freakhill/safeslop \
	       internal/engine/control/control.proto
```

- [ ] **Step 3: Generate + add the grpc dep.**

```bash
mkdir -p internal/engine/control/pb
make proto
go get google.golang.org/grpc@latest
go mod tidy
```
Expected: `internal/engine/control/pb/control.pb.go` + `control_grpc.pb.go` created; `grpc` now a direct dep.

- [ ] **Step 4: Write a smoke test** — create `internal/engine/control/pb/pb_smoke_test.go`:

```go
package pb

import "testing"

func TestGeneratedTypesExist(t *testing.T) {
	_ = &PingResponse{Version: "x"}
	_ = &LaunchEvent{Kind: LaunchEvent_SPAWNED, ExitCode: 0}
	_ = &ListProfilesResponse{Profiles: []*Profile{{Name: "p"}}}
	if LaunchEvent_EXITED == LaunchEvent_SPAWNED {
		t.Fatal("enum values collapsed")
	}
}
```

- [ ] **Step 5: Run it + the build**

```bash
go test ./internal/engine/control/pb/ -v
go build ./...
```
Expected: PASS; build green (grpc resolves).

- [ ] **Step 6: Commit** (generated code included)

```bash
git add internal/engine/control/control.proto internal/engine/control/pb/ Makefile go.mod go.sum
git commit -m "feat(control): gRPC control.proto + generated Go stubs + make proto"
```

---

### Task 2: `userconfig` — `~/.config/slop/config.cue`

**Files:**
- Create: `internal/engine/userconfig/schema/config.cue`, `internal/engine/userconfig/userconfig.go`
- Test: `internal/engine/userconfig/userconfig_test.go`

- [ ] **Step 1: Write the failing test** — create `internal/engine/userconfig/userconfig_test.go`:

```go
package userconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func loadStr(t *testing.T, src string) (*Config, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.cue")
	if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(p)
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := loadStr(t, "package slopcfg\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Terminal != "Terminal.app" || cfg.Shell != "" || cfg.Tag.OSCTitle != true {
		t.Fatalf("defaults wrong: %+v", cfg)
	}
}

func TestLoadExplicit(t *testing.T) {
	cfg, err := loadStr(t, `package slopcfg
terminal: "Ghostty"
shell: "/bin/zsh"
tag: {oscTitle: false, promptMarker: true}`)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Terminal != "Ghostty" || cfg.Shell != "/bin/zsh" || cfg.Tag.OSCTitle || !cfg.Tag.PromptMarker {
		t.Fatalf("parsed wrong: %+v", cfg)
	}
}

func TestLoadMissingFileIsDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.cue"))
	if err != nil || cfg.Terminal != "Terminal.app" {
		t.Fatalf("missing file must yield defaults: cfg=%+v err=%v", cfg, err)
	}
}

func TestLoadRejectsUnknownTerminal(t *testing.T) {
	if _, err := loadStr(t, `package slopcfg
terminal: "Hyper"`); err == nil {
		t.Fatal("unknown terminal must be rejected by the schema")
	}
}
```

- [ ] **Step 2: Run it, verify it fails**

```bash
go test ./internal/engine/userconfig/ -v
```
Expected: FAIL — package/`Config`/`Load` undefined.

- [ ] **Step 3: Write the schema** — create `internal/engine/userconfig/schema/config.cue`:

```cue
package slopcfg

// User-level slop preferences (~/.config/slop/config.cue) — distinct from per-repo slop.cue.
terminal: "Terminal.app" | "Ghostty" | "generic" | *"Terminal.app"
shell?:   string
tag: {
	oscTitle:     bool | *true
	promptMarker: bool | *false
}
</cue>
```

> Remove the trailing `</cue>` line — that is a copy artifact; the file ends after the closing `}`.

- [ ] **Step 4: Write the loader** — create `internal/engine/userconfig/userconfig.go`:

```go
// Package userconfig loads the user-level ~/.config/slop/config.cue (terminal-launch
// preferences), validated against an embedded CUE schema. Distinct from policy.slop.cue.
package userconfig

import (
	"os"
	"strings"

	_ "embed"

	"cuelang.org/go/cue/cuecontext"
)

//go:embed schema/config.cue
var schemaSrc string

// Config is the resolved user preferences.
type Config struct {
	Terminal string
	Shell    string
	Tag      Tag
}

// Tag controls session recognizability.
type Tag struct {
	OSCTitle     bool
	PromptMarker bool
}

// Load reads + validates path against the embedded schema. A missing file yields defaults.
func Load(path string) (*Config, error) {
	user := "package slopcfg\n"
	if b, err := os.ReadFile(path); err == nil {
		s := string(b)
		if !strings.Contains(s, "package ") {
			s = "package slopcfg\n" + s
		}
		user = s
	}
	ctx := cuecontext.New()
	v := ctx.CompileString(schemaSrc).Unify(ctx.CompileString(user))
	if err := v.Validate(); err != nil {
		return nil, err
	}
	cfg := &Config{Tag: Tag{OSCTitle: true}}
	if t, err := v.LookupPath(cuePath("terminal")).String(); err == nil {
		cfg.Terminal = t
	}
	if sh, err := v.LookupPath(cuePath("shell")).String(); err == nil {
		cfg.Shell = sh
	}
	if b, err := v.LookupPath(cuePath("tag", "oscTitle")).Bool(); err == nil {
		cfg.Tag.OSCTitle = b
	}
	if b, err := v.LookupPath(cuePath("tag", "promptMarker")).Bool(); err == nil {
		cfg.Tag.PromptMarker = b
	}
	return cfg, nil
}
```

Add the `cuePath` helper at the bottom of the file (kept tiny so the import set stays minimal):

```go
func cuePath(parts ...string) cue.Path { return cue.MakePath(pathSels(parts)...) }
```

> Implementation note: import `"cuelang.org/go/cue"` and build selectors with `cue.Str(p)`. Concretely, replace the two helpers above with a single inline form used at each lookup, e.g. `v.LookupPath(cue.MakePath(cue.Str("terminal")))` and `v.LookupPath(cue.MakePath(cue.Str("tag"), cue.Str("oscTitle")))`. Drop the `cuePath`/`pathSels` helpers — they are only sketched here; use `cue.MakePath(cue.Str(...))` directly at each call site. Mirror the exact lookup idiom already used in `internal/engine/policy/policy.go` (grep it: `grep -n LookupPath internal/engine/policy/policy.go`).

- [ ] **Step 5: Run it, verify it passes**

```bash
go test ./internal/engine/userconfig/ -v
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
gofmt -w internal/engine/userconfig/userconfig.go internal/engine/userconfig/userconfig_test.go
git add internal/engine/userconfig/
git commit -m "feat(userconfig): load ~/.config/slop/config.cue (terminal/shell/tagging)"
```

---

### Task 3: terminal adapters + launch core (argv + tagging)

**Files:**
- Create: `internal/engine/launch/launch.go`
- Test: `internal/engine/launch/launch_test.go`

- [ ] **Step 1: Write the failing test** — create `internal/engine/launch/launch_test.go`:

```go
package launch

import (
	"strings"
	"testing"
)

func TestTaggingEnv(t *testing.T) {
	env := taggingEnv("review", "/work/repo", true)
	joined := strings.Join(env, " ")
	for _, want := range []string{"SLOP_SESSION=review", "SLOP_CWD=/work/repo"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("env missing %q: %v", want, env)
		}
	}
	if got := taggingEnv("review", "/w", false); len(got) != 2 {
		t.Fatalf("env always carries the 2 SLOP_* vars: %v", got)
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
```

- [ ] **Step 2: Run it, verify it fails**

```bash
go test ./internal/engine/launch/ -v
```
Expected: FAIL — undefined `taggingEnv`/`adapterArgv`.

- [ ] **Step 3: Write the adapters** — create `internal/engine/launch/launch.go`:

```go
// Package launch spawns a terminal window running an agent, with the ctty handoff intact.
// Adapters turn a shell command into the argv that opens it in the user's preferred terminal.
package launch

// taggingEnv returns the recognizability env injected into the child (always the two SLOP_*
// vars; oscTitle is emitted by the spawned shell wrapper, not here).
func taggingEnv(session, cwd string, oscTitle bool) []string {
	_ = oscTitle
	return []string{"SLOP_SESSION=" + session, "SLOP_CWD=" + cwd}
}

// adapterArgv builds the argv that opens `command` in the named terminal. Unknown terminals
// fall back to the generic `open -a` adapter.
func adapterArgv(terminal, command, session string) []string {
	switch terminal {
	case "Terminal.app":
		script := `tell application "Terminal" to do script "` + command + `"`
		return []string{"osascript", "-e", script}
	case "Ghostty":
		return []string{"open", "-na", "Ghostty", "--args", "-e", command}
	default: // "generic" and any unknown value
		return []string{"open", "-a", "Terminal", "--args", command}
	}
}
```

- [ ] **Step 4: Run it, verify it passes**

```bash
go test ./internal/engine/launch/ -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/engine/launch/launch.go internal/engine/launch/launch_test.go
git add internal/engine/launch/
git commit -m "feat(launch): terminal adapters (Terminal.app/Ghostty/generic) + tagging env"
```

---

### Task 4: peer-auth — same-uid `LOCAL_PEERCRED`

**Files:**
- Create: `internal/engine/control/peerauth.go`
- Test: `internal/engine/control/peerauth_test.go`

- [ ] **Step 1: Write the failing test** — create `internal/engine/control/peerauth_test.go`:

```go
package control

import (
	"net"
	"os"
	"testing"
)

func TestPeerUIDOnSocketpair(t *testing.T) {
	// a connected unix socketpair: both ends are this process => peer uid == our uid.
	c1, c2 := net.Pipe()
	_ = c1
	_ = c2
	// net.Pipe isn't a real fd; use a real unix socket instead.
	dir := t.TempDir()
	addr := dir + "/s.sock"
	ln, err := net.Listen("unix", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan struct{})
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			uid, err := peerUID(conn.(*net.UnixConn))
			if err != nil {
				t.Errorf("peerUID: %v", err)
			} else if uid != os.Getuid() {
				t.Errorf("peer uid = %d, want %d", uid, os.Getuid())
			}
			conn.Close()
		}
		close(done)
	}()
	c, err := net.Dial("unix", addr)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	<-done
}
```

- [ ] **Step 2: Run it, verify it fails**

```bash
go test ./internal/engine/control/ -run TestPeerUID -v
```
Expected: FAIL — `peerUID` undefined.

- [ ] **Step 3: Write peer-auth** — create `internal/engine/control/peerauth.go`:

```go
package control

import (
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// peerUID returns the uid of the connected peer of a unix socket via LOCAL_PEERCRED (darwin).
func peerUID(c *net.UnixConn) (int, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return -1, err
	}
	var xucred *unix.Xucred
	var gerr error
	if err := raw.Control(func(fd uintptr) {
		xucred, gerr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	}); err != nil {
		return -1, err
	}
	if gerr != nil {
		return -1, gerr
	}
	return int(xucred.Uid), nil
}

// authorizePeer rejects any peer whose uid differs from this process's uid (same-user only).
func authorizePeer(c *net.UnixConn) error {
	uid, err := peerUID(c)
	if err != nil {
		return fmt.Errorf("peer cred check: %w", err)
	}
	if uid != os.Getuid() {
		return fmt.Errorf("peer uid %d != server uid %d — cross-user control-plane access denied", uid, os.Getuid())
	}
	return nil
}
```

```bash
go get golang.org/x/sys/unix
go mod tidy
```

- [ ] **Step 4: Run it, verify it passes**

```bash
go test ./internal/engine/control/ -run TestPeerUID -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/engine/control/peerauth.go internal/engine/control/peerauth_test.go
git add internal/engine/control/peerauth.go internal/engine/control/peerauth_test.go go.mod go.sum
git commit -m "feat(control): same-uid LOCAL_PEERCRED peer-auth for the UDS control plane"
```

---

### Task 5: `Control` server impl + `slop serve` + `slop launch`

**Files:**
- Create: `internal/engine/control/server.go`, `internal/engine/control/serve.go`
- Modify: `internal/cli/cli.go`, `internal/cli/cli_test.go`

- [ ] **Step 1: Write the failing test** — create/append `internal/engine/control/server_test.go`:

```go
package control

import (
	"context"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/control/pb"
)

func TestServerPing(t *testing.T) {
	s := &server{version: "vTEST"}
	resp, err := s.Ping(context.Background(), &pb.PingRequest{})
	if err != nil || resp.Version != "vTEST" {
		t.Fatalf("Ping = %+v err=%v", resp, err)
	}
}

func TestSocketPathIsShort(t *testing.T) {
	p, err := socketPath()
	if err != nil {
		t.Fatal(err)
	}
	if len(p) >= 104 {
		t.Fatalf("socket path %q exceeds the 104-byte sun_path limit (%d)", p, len(p))
	}
	if !strings.HasSuffix(p, "/.slop/s.sock") {
		t.Fatalf("socket path = %q, want ~/.slop/s.sock", p)
	}
}
```

> Add `"strings"` to the test imports.

- [ ] **Step 2: Run it, verify it fails**

```bash
go test ./internal/engine/control/ -run 'TestServerPing|TestSocketPathIsShort' -v
```
Expected: FAIL — `server`/`socketPath` undefined.

- [ ] **Step 3: Write the server + serve loop.**

Create `internal/engine/control/server.go`:

```go
package control

import (
	"context"

	"github.com/freakhill/safeslop/internal/engine/control/pb"
)

// server implements pb.ControlServer. Launch delegation is wired in serve.go via launchFn.
type server struct {
	pb.UnimplementedControlServer
	version  string
	launchFn func(profile, configPath string, emit func(*pb.LaunchEvent)) error
}

func (s *server) Ping(_ context.Context, _ *pb.PingRequest) (*pb.PingResponse, error) {
	return &pb.PingResponse{Version: s.version}, nil
}

func (s *server) Launch(req *pb.LaunchRequest, stream pb.Control_LaunchServer) error {
	emit := func(e *pb.LaunchEvent) { _ = stream.Send(e) }
	if s.launchFn == nil {
		emit(&pb.LaunchEvent{Kind: pb.LaunchEvent_ERROR, Message: "launch not wired"})
		return nil
	}
	return s.launchFn(req.Profile, req.ConfigPath, emit)
}
```

> `ListProfiles` is left on `UnimplementedControlServer` for SP7a (the portal's profile list is wired when the app lands; add a concrete impl here in a follow-on). This keeps SP7a's server minimal but real.

Create `internal/engine/control/serve.go`:

```go
package control

import (
	"fmt"
	"net"
	"os"
	"path/filepath"

	"google.golang.org/grpc"

	"github.com/freakhill/safeslop/internal/engine/control/pb"
)

// socketPath is ~/.slop/s.sock — deliberately short (macOS sun_path is 104 bytes).
func socketPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".slop", "s.sock"), nil
}

// Serve binds the UDS (0700 dir / 0600 socket), enforces same-uid peer-auth, and serves the
// Control service until the listener is closed. version is reported by Ping; launchFn handles
// Launch RPCs.
func Serve(version string, launchFn func(profile, configPath string, emit func(*pb.LaunchEvent)) error) error {
	path, err := socketPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	_ = os.Remove(path) // clear a stale socket from a prior crash
	ln, err := net.Listen("unix", path)
	if err != nil {
		return fmt.Errorf("bind %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	gs := grpc.NewServer()
	pb.RegisterControlServer(gs, &server{version: version, launchFn: launchFn})
	return gs.Serve(peerAuthListener{ln})
}

// peerAuthListener rejects cross-uid peers at Accept time (before any RPC is served).
type peerAuthListener struct{ net.Listener }

func (l peerAuthListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	if uc, ok := c.(*net.UnixConn); ok {
		if err := authorizePeer(uc); err != nil {
			_ = c.Close()
			return l.Accept() // skip the unauthorized peer, keep serving
		}
	}
	return c, nil
}
```

In `internal/cli/cli.go`, add the two cobra commands (wire `slop launch` and `slop serve`; both call a shared `launchProfile` that uses `userconfig` + `launch` + the ctty path). Add to the command registration (next to `cmdDoctor()` etc.) and define:

```go
func cmdServe() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the gRPC control plane on ~/.slop/s.sock (for the GUI app)",
		RunE: func(_ *cobra.Command, _ []string) error {
			return control.Serve(Version, func(profile, configPath string, emit func(*pb.LaunchEvent)) error {
				code, err := launchProfile(profile, configPath)
				if err != nil {
					emit(&pb.LaunchEvent{Kind: pb.LaunchEvent_ERROR, Message: err.Error()})
					return nil
				}
				emit(&pb.LaunchEvent{Kind: pb.LaunchEvent_EXITED, ExitCode: int32(code)})
				return nil
			})
		},
	}
}

func cmdLaunch() *cobra.Command {
	return &cobra.Command{
		Use:   "launch <profile>",
		Short: "Open a terminal window running the profile's agent (ctty intact)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			_, err := launchProfile(args[0], "")
			return err
		},
	}
}
```

Add a `launchProfile(name, configPath string) (int, error)` helper near `runProfile` that: loads `~/.config/slop/config.cue` via `userconfig.Load` (default path `os.UserHomeDir()+"/.config/slop/config.cue"`), resolves the profile (reuse the `policy.Load` + profile lookup that `runProfile` uses), builds the agent command string, then spawns the terminal via `engexec.RunInTerminal` with `launch.adapterArgv(cfg.Terminal, command, name)` and the tagging env. (Mirror how `runProfile`'s `host` case builds argv + env; the difference is wrapping it in the terminal adapter.)

> Register `cmdServe()` and `cmdLaunch()` wherever the root command adds subcommands (grep `AddCommand` in `internal/cli/cli.go`). Import `control` and its `pb` package.

- [ ] **Step 4: Run it, verify it passes**

```bash
go test ./internal/engine/control/ -v && go build ./... && go test ./internal/cli/ -v
```
Expected: PASS; build green.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/engine/control/ internal/cli/cli.go internal/cli/cli_test.go
git add internal/engine/control/server.go internal/engine/control/serve.go internal/engine/control/server_test.go internal/cli/cli.go internal/cli/cli_test.go
git commit -m "feat(cli): slop serve (UDS gRPC, peer-auth) + slop launch (terminal spawn)"
```

---

### Task 6: full verification + PR

- [ ] **Step 1: Full suite + gates.**

```bash
make check && make build
fish -n scripts/*.fish
fish tests/run.fish
fish scripts/slop-sync-help.fish check
fish scripts/slop-pinning.fish
```
Expected: all green. (`slop-sync-help` may require README/AUTOGEN updates for the two new commands — if it flags drift, add `slop serve`/`slop launch` to the README command list per `scripts/CONVENTIONS.md`, then re-run.)

- [ ] **Step 2: Confirm generated code + grpc are committed and CI-safe.**

```bash
git status --short   # nothing uncommitted
grep -q 'google.golang.org/grpc' go.mod && echo "grpc pinned"
```

- [ ] **Step 3: Push + PR.**

```bash
git push -u origin sp7a-control-plane
gh pr create --title "SP7a: gRPC control plane (slop serve) + slop launch + terminal-launch config" --body "$(cat <<'EOF'
## Summary
Implements SP7a (design specs/0012): the engine side of the SP7 GUI.
- `internal/engine/control`: `control.proto` (committed generated Go stubs; `make proto`), `slop serve` binding `~/.slop/s.sock` (0700/0600, short path < 104-byte sun_path), **same-uid LOCAL_PEERCRED peer-auth**, and a `Control` service (`Ping`, streaming `Launch`).
- `internal/engine/userconfig`: loads `~/.config/slop/config.cue` (terminal/shell/tagging), CUE-validated.
- `internal/engine/launch`: terminal adapters (Terminal.app/Ghostty/generic) + `SLOP_SESSION`/`SLOP_CWD` tagging.
- `slop launch <profile>`: opens a terminal running the agent (ctty intact, reuses §6.2 RunInTerminal). The `Launch` RPC delegates to the same core.

## Deferred (specs/0012)
codesign-identity peer-auth (needs CGO/Security.framework); `ListProfiles` impl + the SwiftUI app; iTerm2-native tagging + kitty/wezterm; SP7b installer.

## Test
`make check` + `make build` green; four fish gates green; peer-auth uid check tested over a real unix socket; pure cores (adapters, config parse) unit-tested.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

## Verification (what "done" means)

- `make check` + `make build` + the four fish gates green; `make proto` regenerates cleanly.
- `slop serve` binds `~/.slop/s.sock` (≤104 bytes, 0600) and rejects cross-uid peers; `Ping` returns the version.
- `slop launch <profile>` opens the preferred terminal running the agent with `SLOP_SESSION`/`SLOP_CWD` set.
- `~/.config/slop/config.cue` is CUE-validated (unknown terminal rejected; missing file → defaults).
- Generated gRPC code + `grpc` dep are committed; CI never runs protoc.

## Deliberately deferred (not here)

- **codesign-identity** peer-auth (v1 is uid-only).
- **`ListProfiles`** concrete impl (portal-facing; lands with the app).
- **SwiftUI `Slop.app`** build/sign/notarize (jojo, Xcode).
- **iTerm2-native** tagging; **kitty/wezterm** adapters.
- **SP7b** `slop install`.
