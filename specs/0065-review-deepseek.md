# 0065 Adversarial Spec Review

Reviewer: automated adversarial pass
Date: 2026-07-02
Spec under review: `specs/0065-session-naming.md` (status: draft)

## BLOCKER

### B1 — Key `R` is already live-bound, not a free candidate

**Evidence:**
- `emacs/safeslop-portal.el:531`: `(define-key map (kbd "R") #'safeslop-portal-run-detached)`
- `emacs/test/safeslop-test.el:90`: `` (should (eq (lookup-key safeslop-portal-mode-map (kbd "R")) #'safeslop-portal-run-detached)) ``
- `emacs/test/safeslop-test.el:577`: `run` (portal-run-detached) asserted in Doom key tests
- `specs/0063-emacs-ergonomics-followups.md` key table row: `| R | run detached (was D) | — | — |`
- `emacs/safeslop-session.el:495`: detail-format help text cites `R detach` in the created-status hint line

**What the spec says:** §7 "pick a free key (candidate: `R`)"

**Reality:** `R` is bound to `safeslop-portal-run-detached` in the portal mode map, tested via ERT, and referenced in the 0063 ergonomics F1–F11 scheme as "run detached." It is not free on any surface. Binding rename to `R` would steal the established "run detached" action from the portal and break the existing test at `test/safeslop-test.el:90`.

**Proposed fix:** Pick a genuinely unbound capital letter. From the specs/0063 key table, the following single capitals are unused across all three surfaces: `B`, `F`, `G`, `H`, `J`, `K`, `M`, `N`, `O`, `Q`, `T`, `U`, `W`, `Y`, `Z`. `N` ("name") is the natural mnemonic and has no collision. Update spec §7 to use `N`, add binding to `safeslop-portal-mode-map`, add to `safeslop-portal--key-hints` alist, update the detail-format inline key-hint text, and add a binding regression test.

---

## SHOULD-FIX

### S1 — ValidateName misses Unicode Format (Cf) characters — display hazard

**Evidence:**
- Spec §4.2: "Reject any Unicode control character (category Cc: includes \n, \r, \t, NUL, and other C0/C1 controls)."
- The spec rejects ONLY Unicode category Cc (Control).
- Dangerous rune classes NOT covered:
  - **Bidi/RTL overrides** (U+202A LEFT-TO-RIGHT EMBEDDING through U+202E RIGHT-TO-LEFT OVERRIDE, U+2066 LEFT-TO-RIGHT ISOLATE through U+2069 POP DIRECTIONAL ISOLATE) — all category **Cf** (Format), not Cc. CVE-2021-42574 (Trojan Source): an RLO character in a session name `...\u202E(gninnur)` would render as `...(running)` in a terminal/Emacs, making a stopped session appear running. This is a known security display attack against code-review and terminal UIs.
  - **Zero-width characters** (U+200B ZERO WIDTH SPACE, U+200C ZERO WIDTH NON-JOINER, U+200D ZERO WIDTH JOINER, U+FEFF ZERO WIDTH NO-BREAK SPACE / BOM) — all Cf. Two sessions `sess-abc123 ZWSP "prod"` and `sess-abc123 "prod"` would be visually indistinguishable in the portal.
  - **Line/paragraph separators** (U+2028 LINE SEPARATOR, U+2029 PARAGRAPH SEPARATOR) are category **Zl/Zp**, not Cc. Go's `encoding/json` escapes these as `\u2028`/`\u2029` since Go 1.7, so the JSONL line protocol is safe. However, after Emacs JSON-parses the envelope, the decoded string would contain the raw characters, which can confuse Emacs buffer rendering.

**Proposed fix:** Expand the rejection set to include all Unicode categories Cf (Format), Zl (Line Separator), Zp (Paragraph Separator), plus explicitly reject any rune with the `unicode.IsControl`-equivalent or a hand-maintained disallow list of unsafe format chars. The minimal safe set: `Cc | Cf | CodePoint == 0x2028 | CodePoint == 0x2029`. Add a test case for at least one bidi override (e.g. `"a\u202Eb"`) rejected with `INVALID_ARGUMENT`.

### S2 — D4 "round-trip losslessly" claim is false for new→old direction

**Evidence:**
- Spec D4: "Old and new binaries round-trip each other's records losslessly."
- `internal/engine/session/session.go`: `Session` struct serialized via `json.MarshalIndent`/`json.Unmarshal`.
- Go's `encoding/json` silently ignores unknown keys during unmarshal.

**The problem:** When a new binary writes a session record with `"name": "Foo"`, and then an OLD binary (without the `Name` field) loads and saves that record, the old binary unmarshals the JSON (ignoring the unknown `name` key), then re-marshals only the fields it knows — dropping `name`. The field is **silently lost**. This is not "lossless." The D4 claim only holds for old→new direction (old records load in new binary with empty `Name`).

**Proposed fix:** Correct D4 to read: "Records created by an old binary load unchanged in a new binary (name empty). Old binaries that touch a named record will silently drop the name on save — this is an expected compatibility trade-off, not lossless." No code change needed; this is a documentation honesty fix.

### S3 — `safeslop-session--detail-format` omits the name field

**Evidence:**
- `emacs/safeslop-session.el:464-501`: `safeslop-session--detail-format` renders lines for Session, Agent, Profile, Workspace, Environment, Network, Status, Lifecycle, Credentials, PID, Exit code, Last error, Socket — but **not Name**.
- The spec §7 only addresses `safeslop-session--annotate` and the portal list. The per-session detail/inspect view (the `i` key from the portal) should also show the human name.

**Proposed fix:** Add a `Name:` line to `safeslop-session--detail-format` after `Session:` and before `Agent:`, only when present:

```elisp
(unless (string-empty-p (field 'name)) (line "Name:" (field 'name)))
```

Include this in spec §7 and in an ERT test.

### S4 — Golden test fixture will drift when `name` is added to `sessionData`

**Evidence:**
- `internal/cli/cli_session_test.go:64-87`: `TestSessionCreateGoldenMatchesEmittedEnvelope` builds a `Session` and asserts `jsoncontract.OK(sessionData(sess))` matches `ok-session-create.golden.json` byte-for-byte.
- When `name` is added to `sessionData` (spec §3: `if sess.Name != "" { out["name"] = sess.Name }`), the test `Session` has `Name: ""`, so `name` is omitted — the golden should still match. But if the golden test is ever updated to test a named session, the fixture must be regenerated.
- More importantly: a developer implementing `sessionData` change sees the golden test PASS (empty name omitted) and may incorrectly conclude no fixture update is needed. Any CLI integration test that creates a session with `--name` and inspects the full envelope (like `TestSessionCreateEmitsContractAndPersistsSafeDefaults`) would be the right place to assert the new field.

**Proposed fix:** Spec §10 or §6 should add: "Regenerate `ok-session-create.golden.json` if the test Session gains a non-empty name."

### S5 — No engine test for name surviving `Stop` transition

**Evidence:**
- Spec §5 engine tests: "name survives MarkRunning and Finish (label independent of lifecycle)."
- `internal/engine/session/session.go`: `Stop()` (line ~250) calls `Get`, mutates the session struct, then `Save(sess)`. Since `Name` is a field on `Session`, it would survive — but this is not tested.
- `MarkRunning` and `Finish` are tested in the proposed test list, but `Stop` is a distinct code path with its own `Save`. If someone ever refactors `Stop` to create a new `Session{}` literal, `Name` would be dropped.

**Proposed fix:** Add "name survives Stop (label persists across lifecycle stop)" to spec §5 engine tests.

### S6 — Missing Emacs helper function `safeslop-session--rename-args` not specified

**Evidence:**
- The Emacs pattern for CLI argv helpers: `safeslop-session--create-args` (el:25), `safeslop-session--create-profile-args` (el:35), `safeslop-session--run-args` (el:174), `safeslop-session--attach-args` (el:178), `safeslop-session--remove-args` (el:408), `safeslop-session--prune-args` (el:412).
- Every other session command has a named helper. The spec ERT section says "rename command assembles the correct argv" but never defines the helper function the test would call.

**Proposed fix:** Specify a `safeslop-session--rename-args` function:

```elisp
(defun safeslop-session--rename-args (session-id name)
  "Return exact argv for renaming SESSION-ID to NAME."
  (list "session" "rename" "--session-id" session-id "--name" name "--output" "json"))
```

Add to spec §7.

---

## NIT

### N1 — 64 runes not bounded by terminal cell width

Spec §4.3: "Enforce max 64 runes post-trim (bounds the portal column; ample for a label)." While 64 runes is reasonable, a name of 64 CJK characters or wide emoji (2 cells each) spans ~128 terminal columns, which could overflow a narrow portal frame. This is minor and rarely encountered; 64 is still a generous but not dangerous limit. No action required — documented for awareness.

### N2 — Spec is vague about portal name placement

Spec §7: "Portal list: show the name (column or prefix) when present — place per the current safeslop-portal.el renderer." The current portal (`emacs/safeslop-portal.el:554-564`) has no name column and 10 existing columns: `[Session Agent Env Net Status PID Age Recipe Image Workspace]`. Adding an 11th column tightens every other field. The spec should explicitly decide: (a) new "Name" column (column creep), (b) prefix inside the Session column e.g. `sess-abc… [my-label]`, or (c) inline after the short-id. A specific design directive prevents implementor guesswork.

### N3 — `cmdSessionCreate` explicit-agent branch has multiple saves

The spec says "apply in BOTH creation branches: after the Session exists, if --name was given, name, err := engsession.ValidateName(name); on error emit INVALID_ARGUMENT; else sess.Name = name and store.Save(sess)." The explicit-agent branch already has a conditional `store.Save(sess)` for `--network`. Adding `--name` creates a second conditional Save. If both `--network` and `--name` are given, the session is saved three times (Create → network Save → name Save). Functionally correct but wasteful. Consider batching: apply all post-create mutations then Save once.

### N4 — Detail-format key hint text references `R` for detach

`safeslop-session.el:495`: `"Next: RET/r run coupled · R detach · s stop/revoke · P portal"` — the inline key hint text mentions `R` as detach. If the rename binding uses a different key (as B1 requires), this text is unaffected. But if the author had intended to also bind rename in the detail view, that key hint line needs updating. Probably just a documentation note.

### N5 — Spec should mention updating `safeslop--last-call-status` pattern for rename

The rename Emacs command pattern should follow `safeslop-session-remove` (which takes optional `callback` and `quiet` params) rather than the older stop pattern. Mention this explicitly in §7 so the portal row action path is covered.

---

## VERDICT

**SPEC NEEDS REWORK** — One blocker (key collision on `R`) plus six should-fixes including a security-grade validation gap (Unicode Cf characters bypass the Cc-only reject list) and a false backward-compat claim. The core design (label-not-identity, omitempty, JSONL safety intent) is sound, but the spec has gaps that would produce broken or dangerous code if implemented as-written.
