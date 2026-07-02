# specs/0065 — Session naming and rename

Status: reviewed (flo-evaluator-deepseek pass folded in — see specs/0065-review-deepseek.md)
Branch: `feat/session-naming`
Depends on / references: specs/0050 (session runtime), specs/0051 (detached
supervisor: socket/PID derivation), specs/0063 F7 (forward-referenced a
human-facing session name).

## 1. Motivation

Sessions are identified by an opaque `sess-<hex>` id. An operator running
several sessions in the Emacs portal cannot tell them apart at a glance. This
adds an **optional human display name** that can be set at create time and
changed (or cleared) later via `session rename`, surfaced in the JSON contract
and the portal list.

## 2. Design decisions (boundaries)

These are the load-bearing calls. They keep the feature off the
identity/wire-format hazard path, so no ayo→FLO is required.

- **D1 — Name is a label, never identity.** Sessions remain addressed **only**
  by their `sess-<hex>` id. A rename changes *nothing* derived: not the id, not
  the `.json` record path, not the `SocketPath` (specs/0051), not the PID. This
  is why the change is safe and additive.
- **D2 — No uniqueness enforcement.** Two sessions may carry the same display
  name; the id disambiguates. Enforcing uniqueness would add cross-record
  locking and a new failure mode for what is a pure label — not worth it.
- **D3 — Name is not an addressing handle.** `attach`/`stop`/`rm`/`run` keep
  `--session-id`; you cannot target a session by name. Avoids ambiguity under
  name collisions and preserves the identity boundary (D1).
- **D4 — Additive wire field.** `name` is `omitempty`. Old→new is lossless:
  records written by an old binary (no name) load unchanged in a new binary
  (empty `Name`), and clients ignore an absent field. New→old is **lossy by
  design**: an old binary that loads a *named* record and re-`Save`s it drops the
  unknown `name` key (Go `encoding/json` ignores unknown fields on unmarshal and
  cannot re-emit them). Accepted trade-off for a pure label — not a full
  round-trip guarantee.
- **D5 — Rename is status-independent.** A label change touches no boundary,
  credential, or process state, so it is allowed in **any** status
  (`created` / `running` / `stopped`). Contrast `rm`, which refuses `running`.

## 3. Wire format

- Add `Name string \`json:"name,omitempty"\`` to `session.Session`
  (`internal/engine/session/session.go`), grouped near `Profile`.
- `sessionData` (`internal/cli/cli.go`) surfaces `name` **only when non-empty**,
  mirroring the existing `profile`/`image` handling:
  `if sess.Name != "" { out["name"] = sess.Name }`.

## 4. Validation — `session.ValidateName`

The status/list surface is **JSONL**: one session envelope per line, rendered in
a terminal / Emacs buffer. A name must not corrupt the line protocol or the
display. This is the one hard correctness rule of the feature.

`func ValidateName(raw string) (string, error)`:

1. Trim leading/trailing ASCII whitespace.
2. **Reject any unsafe rune** — not just Unicode category Cc (controls: `\n`,
   `\r`, `\t`, NUL, DEL, C0/C1) but also **Cf (Format)** and the line/paragraph
   separators **Zl/Zp**. In Go: reject when
   `unicode.In(r, unicode.Cc, unicode.Cf, unicode.Zl, unicode.Zp)`. Rationale — a
   name is echoed into a terminal / Emacs buffer and the JSONL status line:
   - Cf covers the **bidi overrides** U+202A–202E / U+2066–2069 (Trojan Source,
     CVE-2021-42574): an RLO in a name can make a *stopped* session render as
     `(running)` — a real display-spoof, not theoretical.
   - Cf also covers **zero-width** chars (U+200B/200C/200D/FEFF) that make two
     different names visually identical in the portal.
   - Zl/Zp (U+2028/U+2029): Go escapes these in JSON, but the decoded string
     still confuses Emacs buffer rendering.
3. Enforce **max 64 runes** post-trim (ample for a label). Note (N1): 64 wide
   (CJK/emoji) runes are ~128 terminal cells, so the portal must truncate for
   display — do not assume 1 rune ≈ 1 cell.
4. Empty / whitespace-only is **valid** and means "no name" (clear). Returns
   `("", nil)`.
5. Otherwise any printable Unicode is allowed (spaces, ordinary punctuation,
   emoji) — it is a human label, not an id.
6. Returns the cleaned (trimmed) name. Validation failures surface as
   `INVALID_ARGUMENT`.

Exported so the CLI reuses the exact same rule at create time and at rename.

## 5. Engine — `internal/engine/session/session.go`

- Add the `Name` field (§3).
- Add `ValidateName` (§4).
- Add `func (s Store) Rename(id, name string, now time.Time) (Session, error)`:
  - `Get(id)` — preserves `ErrNotFound`.
  - `name, err = ValidateName(name)` — return the validation error unchanged.
  - `sess.Name = name`; `sess.UpdatedAt = now.UTC()`; `Save`; return updated
    session.
  - No status guard (D5).

Tests (`session_test.go`, hermetic, temp dir store):
- rename round-trip (set then Get shows name);
- clear (rename to "" ⇒ Name empty, omitted on marshal);
- reject `"a\nb"` / other Cc controls (error, record unchanged on disk);
- reject a **bidi override** `"a\u202Eb"` and a **zero-width** `"a\u200Bb"`
  (validation error) — the S1 security cases;
- reject 65-rune name; accept 64-rune;
- trims surrounding whitespace;
- rename unknown id ⇒ `ErrNotFound`;
- name survives `MarkRunning`, `Finish`, **and `Stop`** (each is a distinct
  `Save` path — label independent of lifecycle);
- backward-compat: a record written without `name` loads with empty `Name`.

## 6. CLI — `internal/cli/cli.go`

- **`cmdSessionCreate`**: add `--name` flag. Apply in **both** creation
  branches (explicit-agent and `--profile`): after the `Session` exists, if
  `--name` was given, `name, err := engsession.ValidateName(name)` (on error
  emit `INVALID_ARGUMENT`); set `sess.Name = name`. **Batch post-create
  mutations and `Save` once** (N3): the explicit-agent branch already saves
  conditionally for `--network`, so fold `name` into a single trailing `Save`
  rather than adding a second one. `--name` **is** combinable with `--profile`
  (orthogonal metadata) — do **not** add it to the profile-exclusivity guard.
- **New `cmdSessionRename()`**, mirroring `cmdSessionRemove`:
  - `Use: "rename --session-id <id> --name <name> --output json"`.
  - `Args: cobra.NoArgs`; require `--output json` (usage error otherwise, like
    the sibling subcommands).
  - Flags: `--session-id` (required; empty ⇒ `INVALID_ARGUMENT` before touching
    the store), `--name` (may be empty ⇒ clears), `--output`.
  - `sess, err := sessionStore().Rename(id, name, time.Now())`; map
    `ErrNotFound → SESSION_NOT_FOUND`, `ValidateName` error →
    `INVALID_ARGUMENT`, else `IO_ERROR`.
  - Success: `emitContract(jsoncontract.OK(sessionData(sess)))` (same envelope
    shape create/status return).
  - Register in the `AddCommand(...)` call (cli.go:432) alongside `rm`/`prune`.
- **`sessionData`**: surface `name` (§3).
- Refresh any `session`/subcommand `Use`/help text touched.

Tests (`cli_session_test.go`, contract-level):
- `create --name Foo` ⇒ envelope `name == "Foo"`;
- `create --name "a\nb"` ⇒ `INVALID_ARGUMENT`;
- `create --profile <p> --name Foo` ⇒ name applied (not rejected by guard);
- `rename` happy path ⇒ envelope name changed;
- `rename --name ""` ⇒ name cleared (absent from envelope);
- `rename` unknown id ⇒ `SESSION_NOT_FOUND`;
- `rename` invalid name ⇒ `INVALID_ARGUMENT`;
- `rename` missing `--output json` ⇒ usage error.

Fixture note (S4): the existing `TestSessionCreateGoldenMatchesEmittedEnvelope`
uses a name-less Session, so `ok-session-create.golden.json` still matches (name
omitted). Assert the new `name` field via a *named* create test
(`create --name Foo` envelope), not by mutating the golden fixture; regenerate
the golden only if its fixture Session is ever given a name.

## 7. Emacs — `emacs/safeslop-session.el` (+ `safeslop-portal.el`)

- `safeslop-session--annotate` already renders a `name` field when present
  (per handoff): **verify against source** and keep; adjust if the key differs.
- Add argv helper (S6) mirroring `safeslop-session--remove-args` (el:408):
  ```elisp
  (defun safeslop-session--rename-args (session-id name)
    (list "session" "rename" "--session-id" session-id "--name" name
          "--output" "json"))
  ```
- Add interactive **`safeslop-session-rename`**, following the
  `safeslop-session-remove` pattern (optional `callback`/`quiet` params, N5) so
  the portal row-action path is covered: resolve the target session id (portal
  point / completion), read the new name in the minibuffer (default = current
  name), invoke the CLI via the helper, refresh the portal on `ok`, surface the
  contract error otherwise. Empty input clears.
- **Key: `N`** (mnemonic "name"). `R` is already bound to
  `safeslop-portal-run-detached` (portal.el:531; specs/0063 "run detached"; ERT
  test.el:90), so it is NOT free (B1). Bind `N` in `safeslop-portal-mode-map`,
  add it to `safeslop-portal--key-hints`, and confirm the Doom key layer. The
  subagent must re-confirm `N` is unbound on every surface before binding.
- **Detail view (S3):** add a `Name:` line to `safeslop-session--detail-format`
  (el:464-501), rendered only when present, placed right after `Session:`
  (e.g. `(unless (string-empty-p (field 'name)) (line "Name:" (field 'name)))`).
  Update the inline key-hint line (el:495) if a rename key is offered there.
- **Portal list placement (N2):** do **not** add an 11th column (the row is
  already 10 wide: Session Agent Env Net Status PID Age Recipe Image Workspace).
  Render the name **inside the Session cell** as a suffix when present, e.g.
  `sess-abcd… my-label`, truncating to the cell width (N1). One explicit
  directive — no implementor guesswork.

ERT (`emacs/test/`): `safeslop-session--rename-args` returns the exact argv;
`N` is bound to the rename command in `safeslop-portal-mode-map` (B1 regression —
and the existing `R`→run-detached assertion at test.el:90 must still pass);
`safeslop-session--annotate` and `--detail-format` show `name`; empty input
clears.

## 8. Docs (sync in the same change — AGENTS.md/CONTRIBUTING.md)

- `README.md`: document `session rename`, `create --name`, and name display.
- `skills/`: update the session-management workflow with rename + `--name`.
- Keep CLI help examples matching real command output.

## 9. Verification

- `make check` (gofmt/vet, Go unit tests incl. new session + cli tests, full
  ERT incl. new emacs tests, byte-compile gate).
- `make build`.
- Targeted: `go test ./internal/engine/session/... ./internal/cli/...`.

## 10. Execution DAG

`SR (spec review) → S1 engine → S2 cli → S3 emacs → S4 verify → S5 merge/push`.
Chain rationale: S2 needs `Rename`/`ValidateName`; S3 needs the S2 contract
(`session rename` argv + `name` envelope field). Docs/tests land inside each
implementing task per the sync policy.

SR is **done**: flo-evaluator-deepseek's adversarial pass
(`specs/0065-review-deepseek.md`) surfaced 1 blocker (R-key collision → `N`), a
security-grade validation gap (Cf/bidi → expanded reject set), and a false
lossless claim (D4) — all folded above.
