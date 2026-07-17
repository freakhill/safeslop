;;; safeslop-profiles.el --- Policy/profile surface for safeslop -*- lexical-binding: t; -*-

;; Copyright (C) 2026

;; Author: safeslop
;; Package-Requires: ((emacs "32.0"))
;; Keywords: tools, processes, ai

;;; Commentary:

;; The Profiles surface: a tabulated-list view of the profiles defined in the
;; active safeslop.cue, over `safeslop profile list --output json'.  Authoring
;; stays CUE-canonical (specs/0029): editing opens the safeslop.cue itself and
;; validates on save; creating goes through the structured `profile create' CLI
;; (specs/0058 IW4) rather than handwritten snippets; and deletion goes through
;; the validated `profile delete' CLI rather than a fragile client-side rewrite
;; of the guard.  All slow calls are async (specs/0052 #7).  The
;; Env column reuses the shared isolation-tier colouring (safeslop-surface).
;;
;; Ergonomics (CRUD), following mature Emacs list UIs (package.el, magit, dired,
;; ibuffer):
;;   - RET / i  inspect: a read-only detail view rendered from `profile show'
;;     (resolved packages, egress, recipe/image/base) — the safe primary action.
;;   - e        edit: open the CUE file, jumping to the profile's block.
;;   - c        create: a prompt chain that validates the name up front and
;;     confirms before clobbering an existing profile (the CLI is create-OR-
;;     update); on success point lands on the new row.
;;   - C        clone: prefill create from the row's full `profile show' data,
;;     so a variant is one keystroke plus a new name.
;;   - D        delete: completing-read the target (default: row at point),
;;     confirm with a one-line summary, then use the validated engine mutation
;;     and refresh the list in place.
;;   The empty state is persistent in-buffer guidance (specs/0062).
;; Navigation to a profile in the CUE file anchors to the field-opening brace
;; (`name: {'), not a loose word search that would also hit comments, string
;; values, or bundle names.

;;; Code:

(require 'subr-x)
(require 'cl-lib)
(require 'button)
(require 'tabulated-list)
(require 'safeslop-contract)
(require 'safeslop-client)
(require 'safeslop-surface)
(require 'safeslop-output)
(require 'safeslop-session)
(require 'safeslop-profile-evaluation)
(require 'safeslop-profile-compose)

;; Top-level commands live in the entry file, above this layer; they are only
;; referenced late-bound (key press / explicit call after the package loads).
(declare-function safeslop-policy-check-file "safeslop" (file &optional callback))
(declare-function safeslop-doctor "safeslop" ())
(declare-function safeslop-credentials "safeslop-credentials" ())

(defun safeslop-profiles--dispatch-remediation (kind action-id docs-ref)
  "Dispatch KIND/ACTION-ID/DOCS-REF without evaluating engine prose.
Actions only open typed guidance or an existing setup surface; they never edit
CUE, approve trust, or execute engine-provided text.  Credentials requires this
front elsewhere, so this function must not require it back.  It reaches
credentials only late-bound (`fboundp'-guarded) and degrades to typed guidance
when credentials is not loaded, keeping the require graph one-directional
(specs/0062)."
  (if (and (equal kind "operator_workflow")
           (member action-id '("link-github-account" "link-forgejo-account"))
           (fboundp 'safeslop-credentials))
      (progn
        (safeslop-credentials)
        (message "safeslop: use `a' to complete %s; see %s" action-id docs-ref))
    (let ((guidance
           (pcase kind
             ("policy_change" "Review and edit the policy explicitly; no CUE was changed.")
             ("operator_workflow" "Complete the named operator workflow explicitly.")
             ("install_helper" "Install the named helper through a trusted local workflow.")
             ("repair_helper_resolution" "Repair sanitized helper resolution before retrying.")
             ("review_and_trust" "Review the exact policy bytes, then use the explicit trust flow; nothing was approved.")
             ("retry_check" "Retry the engine check after correcting the local prerequisite.")
             (_ "Update safeslop before using this unknown remediation kind."))))
      (with-current-buffer (get-buffer-create "*safeslop remediation guidance*")
        (let ((inhibit-read-only t))
          (special-mode)
          (erase-buffer)
          (insert "safeslop remediation guidance\n\n")
          (insert (format "Kind: %s\nAction: %s\nDocs: %s\n\n%s\n"
                          kind action-id docs-ref guidance)))
        (pop-to-buffer (current-buffer))))))

(defconst safeslop-profiles-buffer-name "*safeslop profiles*"
  "Buffer name for the safeslop profiles surface.")

(defvar-local safeslop-profiles--config-path nil
  "Path to the safeslop.cue backing this buffer, from the last `profile list'.
Edit/validate/delete act on this file; nil until a config is found.")

(defconst safeslop-profiles--agents '("claude" "pi" "fish" "zsh" "shell")
  "Profile-create agent choices surfaced in Emacs.
`claude-code' stays a CLI compatibility alias but is intentionally not a UI
choice; the UI presents the canonical `claude' engine name.")

(defconst safeslop-profiles--environments '("container" "host")
  "Profile-create environment choices; container remains the safe default.")

(defconst safeslop-profiles--networks '("deny" "allow")
  "Profile-create network choices; deny remains the safe default.")

(defun safeslop-profiles--network-label (environment network)
  "Return the compose label for ENVIRONMENT and NETWORK without granting authority.
Container deny remains ordinary deny-by-default policy; the progressive review
label only describes the later, operator-opened session review workflow."
  (if (and (equal environment "container") (equal network "deny"))
      "Deny (progressive review)"
    (or network "deny")))

(defconst safeslop-profiles--name-regexp "\\`[A-Za-z_][A-Za-z0-9_-]*\\'"
  "Regexp a profile name must match to be offered to the CLI.
A leading letter/underscore then letters, digits, underscores, or hyphens.  The
CLI re-validates the rendered CUE, so this is the early, friendly gate (a wrong
name is caught before the rest of the prompt chain), not the only one.")

(defun safeslop-profiles--nonempty-list (values)
  "Return VALUES without empty strings or nils."
  (delq nil (mapcar (lambda (v)
                      (when (and (stringp v) (not (string-empty-p v))) v))
                    values)))

(defun safeslop-profiles--catalog-names (data field)
  "Return catalog entry names from DATA under FIELD (`bundles' or `packages')."
  (mapcar (lambda (entry) (alist-get 'name entry))
          (alist-get field data)))

(defun safeslop-profiles--catalog-choice-list (field &optional bundles)
  "Synchronously fetch catalog FIELD names for an interactive picker.
BUNDLES non-nil calls `catalog list --bundles`; otherwise package entries are
listed.  The catalog is in-tree/local and intentionally fast; the actual profile
create write remains asynchronous."
  (let* ((args (append '("catalog" "list")
                       (when bundles '("--bundles"))
                       '("--output" "json")))
         (env (safeslop--call-json args)))
    (if (safeslop-contract-ok-p env)
        (safeslop-profiles--catalog-names (safeslop-contract-data env) field)
      (message "safeslop: could not list catalog %s: %s"
               field
               (or (alist-get 'message (car (safeslop-contract-errors env)))
                   "catalog list failed"))
      nil)))

(defun safeslop-profiles--read-multiple (prompt choices)
  "Read zero or more CHOICES with PROMPT, normalizing the empty selection to nil."
  (safeslop-profiles--nonempty-list
   (completing-read-multiple prompt choices nil nil)))

(defun safeslop-profiles--repeat-flags (flag values)
  "Return repeated FLAG argv entries for VALUES."
  (apply #'append (mapcar (lambda (v) (list flag v)) values)))

(defun safeslop-profiles--create-args
    (name agent environment bundles packages network workspace &optional no-default-bundle)
  "Return exact argv for `safeslop profile create' from the structured UI fields."
  (append (list "profile" "create"
                "--name" name
                "--agent" agent
                "--environment" environment)
          (safeslop-profiles--repeat-flags "--bundle" (or bundles nil))
          (safeslop-profiles--repeat-flags "--package" (or packages nil))
          (when (and (stringp workspace) (not (string-empty-p workspace)))
            (list "--workspace" workspace))
          (when (and (stringp network) (not (string-empty-p network)))
            (list "--network" network))
          (when no-default-bundle (list "--no-default-bundle"))
          (list "--output" "json")))

(defun safeslop-profiles--rows (data)
  "Build source-labelled rows from `profile list' DATA with project precedence."
  (let ((projects (alist-get 'profiles data))
        rows)
    (dolist (entry projects)
      (let ((name (symbol-name (car entry))) (p (cdr entry)))
        (push (list name (vector name (or (alist-get 'agent p) "")
                                 (safeslop-surface--env-cell (or (alist-get 'environment p) ""))
                                 (safeslop-surface--net-cell (or (alist-get 'network p) ""))
                                 "project")) rows)))
    (dolist (builtin (append (alist-get 'builtins data) nil))
      (let ((name (alist-get 'name builtin)) (p (alist-get 'profile builtin)))
        (unless (assoc (intern name) projects)
          (push (list name (vector name (or (alist-get 'agent p) "")
                                   (safeslop-surface--env-cell (or (alist-get 'environment p) ""))
                                   (safeslop-surface--net-cell (or (alist-get 'network p) ""))
                                   "builtin")) rows))))
    (sort rows (lambda (a b) (string< (car a) (car b))))))

;;; ---- pure CRUD helpers (unit-tested) -------------------------------------

(defun safeslop-profiles--valid-name-p (name)
  "Return non-nil when NAME is an acceptable profile name (see name-regexp)."
  (and (stringp name)
       (string-match-p safeslop-profiles--name-regexp name)))

(defun safeslop-profiles--names ()
  "Return the names of the profiles currently listed in this buffer."
  (mapcar #'car tabulated-list-entries))

(defun safeslop-profiles--join (values)
  "Render VALUES (a list of strings) as a comma list, or \"(none)\" if empty."
  (if (and values (listp values))
      (mapconcat #'identity values ", ")
    "(none)"))

(defun safeslop-profiles--row-fields (name)
  "Return (AGENT ENV NETWORK) for listed profile NAME, stripping text properties."
  (let ((entry (assoc name tabulated-list-entries)))
    (when entry
      (let ((v (cadr entry)))
        (list (substring-no-properties (aref v 1))
              (substring-no-properties (aref v 2))
              (substring-no-properties (aref v 3)))))))

(defun safeslop-profiles--builtin-name-p (name)
  "Return non-nil when NAME identifies an immutable builtin row."
  (let ((entry (assoc name tabulated-list-entries)))
    (equal (and entry (aref (cadr entry) 4)) "builtin")))

(defun safeslop-profiles--row-summary (name)
  "Return a one-line \"agent, env, net\" summary for listed profile NAME, or nil."
  (when-let* ((fields (safeslop-profiles--row-fields name)))
    (format "%s, %s, %s" (nth 0 fields) (nth 1 fields) (nth 2 fields))))

;; The danger summary is shared surface presentation now (specs/0063 F4): the
;; portal's run confirm shows the same text. The alias keeps the profiles-era
;; name working for tests and user config.
(defalias 'safeslop-profiles--danger-summary #'safeslop-surface--danger-summary)

(defun safeslop-profiles--show-args (name)
  "Return argv for `profile show' of NAME, pinned to this buffer's config
when known. The list surface may be opened from one directory and
revisited after `default-directory' changes, so detail/clone operations
must address the same safeslop.cue that `profile list' loaded, not
whatever the current cwd happens to contain."
  (append (list "profile" "show" name)
          (when safeslop-profiles--config-path (list safeslop-profiles--config-path))
          (list "--output" "json")))

(defun safeslop-profiles--copy-name (name existing)
  "Return a non-conflicting clone name for NAME given EXISTING names."
  (let ((candidate (concat name "-copy"))
        (n 2))
    (while (member candidate existing)
      (setq candidate (format "%s-copy-%d" name n))
      (setq n (1+ n)))
    candidate))

(defun safeslop-profiles--normalize-workspace (workspace)
  "Normalize a create prompt WORKSPACE value.
The empty string means \"omit --workspace\" (engine default). A literal `.' is
kept as `.' because it is the common repo-root policy spelling; other non-empty
paths are expanded/abbreviated for stable CUE output."
  (let ((workspace (string-trim (or workspace ""))))
    (cond ((string-empty-p workspace) "")
          ((string= workspace ".") ".")
          (t (abbreviate-file-name (expand-file-name workspace))))))

(defun safeslop-profiles--read-workspace ()
  "Read the optional workspace field, allowing a true empty response."
  (safeslop-profiles--normalize-workspace
   (read-string "Workspace (empty for engine default, . for repo root): " nil nil "")))

(defun safeslop-profiles--block-anchor-regexp (name)
  "Return a regexp matching a line that opens profile NAME's CUE block.
Matches `name: {' or `\"name\": {' at the start of a line (any indent). Callers
scope the search to the `profiles' field first, so a same-named top-level block,
comment, string value, or bundle entry is not mistaken for the profile."
  (concat "^[ \t]*\"?" (regexp-quote name) "\"?[ \t]*:[ \t]*{"))

(defun safeslop-profiles--cue-path-prefix-regexp ()
  "Return a regexp for CUE field prefixes before `profiles'.
This allows compact forms like `safeslop: profiles:' while refusing comment or
free-text lines such as `// profiles:' as structural anchors."
  "^[ \t]*\\(?:\"?[A-Za-z_][A-Za-z0-9_-]*\"?[ \t]*:[ \t]*\\)*")

(defun safeslop-profiles--profiles-anchor-regexp ()
  "Return a regexp matching a structural CUE `profiles:' field."
  (concat (safeslop-profiles--cue-path-prefix-regexp) "\"?profiles\"?[ \t]*:"))

(defun safeslop-profiles--inline-profile-anchor-regexp (name)
  "Return a regexp for compact CUE like `profiles: NAME: { ... }'."
  (concat (safeslop-profiles--profiles-anchor-regexp)
          "[^\n]*\\(\"?" (regexp-quote name) "\"?[ \t]*:[ \t]*{\\)"))

(defun safeslop-profiles--goto-profile-block (name)
  "Move point to the line opening profile NAME's CUE block; return non-nil
if found. The search is scoped to the `profiles' field (including
compact one-line CUE) before matching NAME, avoiding the old loose-word
failure mode that could jump to a top-level or nested same-named block
outside the profile map."
  (goto-char (point-min))
  (or
   (when (re-search-forward (safeslop-profiles--inline-profile-anchor-regexp name) nil t)
     (goto-char (match-beginning 1))
     t)
   (progn
     ;; A failed buffer-wide inline search can leave point after the `profiles'
     ;; block; reset before the block-scoped multi-line search.
     (goto-char (point-min))
     (when (re-search-forward (safeslop-profiles--profiles-anchor-regexp) nil t)
       (when (re-search-forward (safeslop-profiles--block-anchor-regexp name) nil t)
         (beginning-of-line)
         (back-to-indentation)
         t)))))

(defun safeslop-profiles--goto-name (name)
  "Move point to the list row whose id is NAME; return non-nil if found."
  (goto-char (point-min))
  (let ((found nil))
    (while (and (not found) (not (eobp)))
      (if (equal (tabulated-list-get-id) name)
          (setq found t)
        (forward-line 1)))
    found))

(defun safeslop-profiles--inspect-format (data)
  "Format `profile show' DATA as a human-readable inspection string."
  (let* ((name (alist-get 'name data))
         (prof (alist-get 'profile data))
         (resolved (alist-get 'resolved data))
         (env (alist-get 'environment prof))
         (net (alist-get 'network prof))
         (ws (alist-get 'workspace prof)))
    (mapconcat
     #'identity
     (delq nil
           (list
            (format "Profile:     %s" (or name ""))
            (format "Agent:       %s" (or (alist-get 'agent prof) ""))
            (format "Environment: %s" (or env ""))
            (let ((note (nth 3 (assoc env safeslop-surface--env-tiers))))
              (when note (format "Isolation:   %s" note)))
            (format "Network:     %s%s" (or net "deny")
                    (if (equal net "allow") " — egress reachable (deny is the safe default)" ""))
            (when (and (stringp ws) (not (string-empty-p ws)))
              (format "Workspace:   %s" ws))
            (format "Bundles:     %s" (safeslop-profiles--join (alist-get 'bundles prof)))
            (format "Packages:    %s" (safeslop-profiles--join (alist-get 'packages prof)))
            (format "Resolved:    %s" (safeslop-profiles--join (alist-get 'identitySet resolved)))
            (format "Egress:      %s" (safeslop-profiles--join (alist-get 'runtimeEgress resolved)))
            (when (alist-get 'recipeID data) (format "Recipe:      %s" (alist-get 'recipeID data)))
            (when (alist-get 'image data)
              (format "Image:       %s (built on first launch if absent)" (alist-get 'image data)))
            (when (alist-get 'base data) (format "Base:        %s" (alist-get 'base data)))
            ""
            (safeslop-profiles--evaluation-text data)))
     "\n")))

;;; ---- rendering -----------------------------------------------------------

(defconst safeslop-profiles--key-hints
  '(("RET" . "inspect") ("r" . "launch") ("e" . "edit") ("c" . "create")
    ("C" . "clone") ("v" . "validate") ("D" . "delete") ("g" . "refresh")
    ("d" . "doctor") ("E" . "error") ("L" . "debug") ("?" . "help") ("q" . "quit"))
  "Key/action pairs shown in the profiles surface's in-buffer legend.")

(defun safeslop-profiles--legend ()
  "Return the profiles shortcut legend line, trailing blank line."
  (safeslop-surface--legend safeslop-profiles--key-hints))

(defun safeslop-profiles--empty-state (&optional config-path)
  "Return persistent guidance for an empty (but successful) profile listing.
A failed fetch is the render engine's error banner; this covers the two
ok-but-empty cases: a known CONFIG-PATH with no profiles yet, and no
safeslop.cue found at all."
  (if config-path
      (concat (propertize (format "No profiles in %s yet" (abbreviate-file-name config-path))
                          'face 'safeslop-surface-hint)
              " — press " (propertize "c" 'face 'help-key-binding)
              " to add one, or " (propertize "g" 'face 'help-key-binding) " to refresh.\n")
    (concat (propertize "No safeslop.cue found" 'face 'safeslop-surface-hint)
            " — press " (propertize "c" 'face 'help-key-binding)
            " to create one, or " (propertize "g" 'face 'help-key-binding) " to retry.\n")))

(defun safeslop-profiles--header ()
  "Return the profiles header block: tab strip, tier/net legends, shortcuts."
  (concat (safeslop-surface--tab-strip 'profiles)
          (safeslop-surface--tier-legend)
          (safeslop-surface--net-legend)
          (safeslop-profiles--legend)))

(defun safeslop-profiles--render (&optional keep-point then)
  "Fetch the profile list and redraw the current surface buffer in place.
A thin wrapper over the shared `safeslop-surface-render' engine: contributes the
argv, the row builder (which also records the backing safeslop.cue path for the
edit/validate/delete keys), the header, and the missing-config empty state.
KEEP-POINT/THEN are the engine's — THEN is used to land point on a freshly
created profile."
  (safeslop-surface-render
   :argv '("profile" "list" "--output" "json")
   :label "profile list"
   :noun "profiles"
   :header-fn #'safeslop-profiles--header
   :empty-fn (lambda () (safeslop-profiles--empty-state safeslop-profiles--config-path))
   :entries-fn (lambda (envelope)
                 (if (safeslop-contract-ok-p envelope)
                     (let ((data (safeslop-contract-data envelope)))
                       (setq safeslop-profiles--config-path (alist-get 'path data))
                       (safeslop-profiles--rows data))
                   (setq safeslop-profiles--config-path nil)
                   nil))
   :keep-point keep-point
   :then then))

(defun safeslop-profiles-refresh ()
  "Re-fetch the profile list and redraw, keeping point on its profile."
  (interactive)
  (safeslop-profiles--render t))

(defun safeslop-profiles--validate-quietly (path)
  "Validate PATH asynchronously and report ok/error in the echo area (no popup)."
  (safeslop--call-json-async
   (list "validate" (expand-file-name path) "--json")
   (lambda (env)
     (if (safeslop-contract-ok-p env)
         (message "safeslop: %s is valid" (file-name-nondirectory path))
       (message "safeslop: %s — %s" (file-name-nondirectory path)
                (or (alist-get 'message (car (safeslop-contract-errors env))) "invalid"))))))

(defun safeslop-profiles--validate-on-save ()
  "An `after-save-hook' that quietly re-validates the safeslop.cue just saved."
  (when buffer-file-name
    (safeslop-profiles--validate-quietly buffer-file-name)))

(defun safeslop-profiles--open-config (path)
  "Open PATH for editing and install the quiet validate-on-save hook."
  (find-file path)
  (add-hook 'after-save-hook #'safeslop-profiles--validate-on-save nil t))

(defun safeslop-profiles-validate ()
  "Validate the safeslop.cue backing this surface, showing the full envelope."
  (interactive)
  (if safeslop-profiles--config-path
      (safeslop-policy-check-file safeslop-profiles--config-path)
    (user-error "No safeslop.cue known; refresh, or scaffold one with `c'")))

;;; ---- read (inspect) ------------------------------------------------------

(defvar-local safeslop-profiles--inspect-name nil
  "Profile name described by the current inspect buffer.")

(defvar safeslop-profiles-inspect-mode-map
  (let ((map (make-sparse-keymap)))
    (define-key map (kbd "r") #'safeslop-profiles-inspect-launch)
    (define-key map (kbd "e") #'safeslop-profiles-inspect-edit)
    (define-key map (kbd "C") #'safeslop-profiles-inspect-clone)
    (define-key map (kbd "g") #'safeslop-profiles-inspect-refresh)
    (define-key map (kbd "RET") #'safeslop-profiles-inspect-back)
    (define-key map (kbd "q") #'quit-window)
    map)
  "Keymap for profile inspect buffers.")

(defun safeslop-profiles--inspect-legend ()
  "Return profile inspect key help."
  (concat (propertize "r" 'face 'help-key-binding) " launch  "
          (propertize "e" 'face 'help-key-binding) " edit  "
          (propertize "C" 'face 'help-key-binding) " clone  "
          (propertize "g" 'face 'help-key-binding) " refresh  "
          (propertize "RET" 'face 'help-key-binding) " back  "
          (propertize "q" 'face 'help-key-binding) " quit\n\n"))

(defun safeslop-profiles--from-inspect (command)
  "Return to this inspect buffer's row and run COMMAND when non-nil."
  (let ((name safeslop-profiles--inspect-name)
        (buf (get-buffer safeslop-profiles-buffer-name)))
    (unless (buffer-live-p buf)
      (user-error "The profiles list is gone; press `C-c s F' to reopen it"))
    (pop-to-buffer-same-window buf)
    (when name (safeslop-profiles--goto-name name))
    (when command (call-interactively command))))

(defun safeslop-profiles-inspect-launch () (interactive) (safeslop-profiles--from-inspect #'safeslop-profiles-launch))
(defun safeslop-profiles-inspect-edit () (interactive) (safeslop-profiles--from-inspect #'safeslop-profiles-edit))
(defun safeslop-profiles-inspect-clone () (interactive) (safeslop-profiles--from-inspect #'safeslop-profiles-clone))
(defun safeslop-profiles-inspect-back () (interactive) (safeslop-profiles--from-inspect nil))

(defun safeslop-profiles-inspect-refresh ()
  "Re-fetch this buffer's `profile show' and re-render the faced inspect view.
`g' used to mean \"back to the list\" here while meaning refresh everywhere
else (specs/0063 F5); back stays on RET."
  (interactive)
  (let ((name safeslop-profiles--inspect-name)
        (args (or safeslop-profiles--inspect-args
                  (list "profile" "show" safeslop-profiles--inspect-name
                        "--output" "json"))))
    (safeslop--call-json-async
     args
     (lambda (env)
       (if (safeslop-contract-ok-p env)
           (safeslop-profiles--show-inspect name (safeslop-contract-data env) args)
         (message "safeslop: profile show failed: %s"
                  (safeslop-surface--error-message env "profile show failed")))))))

(defvar-local safeslop-profiles--inspect-args nil
  "Exact `profile show' argv behind this inspect buffer, for `g' refresh.
Captured in the list buffer (where `safeslop-profiles--config-path' is known);
the inspect buffer itself has no config path.")

(defun safeslop-profiles--show-inspect (name data &optional args)
  "Render `profile show' DATA for NAME in a read-only actionable detail buffer.
ARGS is the argv that produced DATA, stored for the faced `g' refresh
(specs/0063 F5)."
  (let ((buf (get-buffer-create (format "*safeslop profile %s*" name))))
    (with-current-buffer buf
      (safeslop-output-mode)
      (setq safeslop-profiles--inspect-name name)
      (setq safeslop-profiles--inspect-args args)
      ;; Feed the shared output refresh (raw `g' and Evil `gr') the faced
      ;; re-render instead of the raw envelope dump (specs/0063 F5).
      (setq safeslop-output--args (or args (safeslop-profiles--show-args name))
            safeslop-output--buffer-name (buffer-name))
      (setq safeslop-output--rerender
            (lambda (env)
              (safeslop-profiles--show-inspect name (safeslop-contract-data env) args)))
      (use-local-map (make-composed-keymap safeslop-profiles-inspect-mode-map
                                           safeslop-output-mode-map))
      (let ((inhibit-read-only t))
        (erase-buffer)
        (insert (safeslop-surface--breadcrumb (or args (safeslop-profiles--show-args name))))
        (insert (safeslop-profiles--inspect-legend))
        (insert (safeslop-profiles--inspect-format data))
        (goto-char (point-min))))
    (pop-to-buffer buf)
    buf))

(defun safeslop-profiles-inspect ()
  "Show resolved details for the profile at point in a read-only buffer.
This is the safe primary action (RET): it renders `profile show' — agent,
environment, network, workspace, resolved packages, unioned egress, and the
dry-run recipe/image/base — without touching the CUE file."
  (interactive)
  (let ((name (tabulated-list-get-id))
        args)
    (unless name (user-error "No profile on this line"))
    (setq args (safeslop-profiles--show-args name))
    (safeslop--call-json-async
     args
     (lambda (env)
       (if (safeslop-contract-ok-p env)
           (safeslop-profiles--show-inspect name (safeslop-contract-data env) args)
         (message "safeslop: profile show failed: %s"
                  (or (alist-get 'message (car (safeslop-contract-errors env)))
                      "unknown error")))))))

;;; ---- update (edit) -------------------------------------------------------

(defun safeslop-profiles--show-launch-review (name args data)
  "Show NAME's exact evaluation from DATA before launch confirmation.
ARGS is the `profile show' command that supplied the snapshot."
  (let ((buf (get-buffer-create (format "*safeslop profile %s launch review*" name))))
    (with-current-buffer buf
      (let ((inhibit-read-only t))
        (special-mode)
        (erase-buffer)
        (insert (safeslop-surface--breadcrumb args))
        (insert (format "Launch review for profile: %s\n\n" name))
        (insert (safeslop-profiles--evaluation-text data))
        (goto-char (point-min))))
    (display-buffer buf)
    buf))

(defun safeslop-profiles-launch ()
  "Fetch and review a profile's engine evaluation, then offer session creation.
The subsequent `session create' and `session run' CLI trust/host/network gates
remain authoritative; this review is never an authorization token."
  (interactive)
  (let ((name (tabulated-list-get-id))
        (directory default-directory)
        args)
    (unless name (user-error "No profile on this line"))
    (setq args (safeslop-profiles--show-args name))
    (safeslop--call-json-async
     args
     (lambda (env)
       ;; Async process callbacks do not retain the originating buffer's cwd.
       ;; Keep session create pinned to the same project as profile show.
       (let ((default-directory directory))
         (if (not (safeslop-contract-ok-p env))
             (message "safeslop: profile show failed before launch review: %s"
                      (safeslop-surface--error-message env "profile show failed"))
           (let ((data (safeslop-contract-data env)))
             (safeslop-profiles--show-launch-review name args data)
             (when (yes-or-no-p
                    (format "Launch session from `%s' after reviewing the engine evaluation? " name))
               (safeslop-session-new-from-profile name)))))))))

(defun safeslop-profiles-edit ()
  "Open the active safeslop.cue for editing, jumping to the profile at point.
CUE bytes are the source of truth (specs/0029), so editing is direct; saves are
quietly re-validated."
  (interactive)
  (let ((path safeslop-profiles--config-path)
        (name (tabulated-list-get-id)))
    (when (and name (safeslop-profiles--builtin-name-p name))
      (user-error "Builtin profile `%s' is embedded and immutable; launch it or create a project profile" name))
    (unless path (user-error "No safeslop.cue known; scaffold one with `c'"))
    (safeslop-profiles--open-config path)
    (if (and name (safeslop-profiles--goto-profile-block name))
        (message "Editing `%s' in %s — saves re-validate; `C-c s F' returns to the list"
                 name (file-name-nondirectory path))
      (message "Editing %s — saves re-validate; `C-c s F' returns to the profiles list"
               path))))

;;; ---- delete --------------------------------------------------------------

(defun safeslop-profiles-delete ()
  "Delete a selected profile through the validated engine mutation.
The target is chosen with completion (defaulting to the profile at point) and
requires a final explicit confirmation.  The CLI renders and validates the
complete remaining CUE before writing; on success this Profiles buffer refreshes
in place, so deletion never requires hand-editing a profile block."
  (interactive)
  (let ((path safeslop-profiles--config-path)
        (names (seq-remove #'safeslop-profiles--builtin-name-p (safeslop-profiles--names)))
        (at-point (tabulated-list-get-id)))
    (unless path (user-error "No safeslop.cue known; refresh, or scaffold one with `c'"))
    (unless names (user-error "No profiles to delete"))
    (let* ((name (completing-read
                  (if at-point
                      (format "Delete profile (default %s): " at-point)
                    "Delete profile: ")
                  names nil t nil nil at-point))
           (summary (safeslop-profiles--row-summary name))
           (args (list "profile" "delete" name path "--output" "json"))
           (buffer (current-buffer)))
      (when (yes-or-no-p (format "Delete profile `%s'%s from %s? "
                                 name
                                 (if summary (format " [%s]" summary) "")
                                 (file-name-nondirectory path)))
        (safeslop--call-json-async
         args
         (lambda (env)
           (if (safeslop-contract-ok-p env)
               (progn
                 (message "safeslop: profile `%s' deleted" name)
                 (when (buffer-live-p buffer)
                   (with-current-buffer buffer
                     (safeslop-profiles--render t))))
             (message "safeslop: profile delete failed: %s"
                      (safeslop-surface--error-message env "profile delete failed")))))))))

;;; ---- create / clone ------------------------------------------------------

;;;###autoload
(defun safeslop-profiles-create
    (&optional name agent environment bundles packages network workspace callback no-default-bundle)
  "Create or update a profile through `safeslop profile create'.
Interactively, prompt for NAME (validated; overwriting an existing
profile is confirmed), AGENT, ENVIRONMENT, BUNDLES, PACKAGES, NETWORK,
and WORKSPACE; then write via the CLI and refresh any live profiles
surface, landing point on the new row.  CALLBACK, when given, receives
the resulting JSON contract envelope.  The old preset scaffold is
intentionally replaced by this structured flow (specs/0058 N5), while
CUE remains the stored source of truth."
  (interactive)
  (if (and (called-interactively-p 'interactive)
           (not name) (not agent) (not environment) (not bundles) (not packages)
           (not network) (not workspace) (not callback) (not no-default-bundle))
      (safeslop-profiles-compose-open)
    (let ((args (safeslop-profiles--create-args
               (or name "")
               (or agent "claude")
               (or environment "container")
               bundles packages
               (or network "deny")
               (or workspace "")
               no-default-bundle)))
    (safeslop--call-json-async
     args
     (lambda (env)
       (safeslop--show-envelope-buffer "*safeslop profile create*" args env)
       (if (safeslop-contract-ok-p env)
           (let ((saved (alist-get 'name (safeslop-contract-data env))))
             (message "safeslop: profile `%s' saved" saved)
             (let ((buf (get-buffer safeslop-profiles-buffer-name)))
               (when buf
                 (with-current-buffer buf
                   (safeslop-profiles--render
                    t (lambda () (safeslop-profiles--goto-name saved)))))))
         (message "safeslop: profile create failed: %s"
                  (or (alist-get 'message (car (safeslop-contract-errors env)))
                      "unknown error")))
       (when callback (funcall callback env)))))))

(defalias 'safeslop-profiles-new #'safeslop-profiles-create
  "Compatibility alias for `safeslop-profiles-create'.")

(defun safeslop-profiles-clone ()
  "Clone the profile at point: prefill create from its full `profile show'
data. Only the new name is prompted (defaulting to NAME-copy); agent,
environment, network, workspace, bundles, packages, and the bare-agent
opt-out are copied from the source, so a variant is one keystroke plus a
name.  The write still goes through `profile create'."
  (interactive)
  (let ((name (tabulated-list-get-id))
        (existing (safeslop-profiles--names)))
    (when (and name (safeslop-profiles--builtin-name-p name))
      (user-error "Builtin profile `%s' is embedded and cannot be cloned through the partial compose UI" name))
    (unless name (user-error "No profile on this line"))
    (safeslop--call-json-async
     (safeslop-profiles--show-args name)
     (lambda (env)
       (if (not (safeslop-contract-ok-p env))
           (message "safeslop: could not read `%s' to clone: %s" name
                    (or (alist-get 'message (car (safeslop-contract-errors env)))
                        "profile show failed"))
         (let* ((prof (alist-get 'profile (safeslop-contract-data env)))
                (newname (safeslop-profiles--read-name
                          existing (safeslop-profiles--copy-name name existing)))
                (agent (or (alist-get 'agent prof) "claude"))
                (environment (or (alist-get 'environment prof) "container"))
                (bundles (alist-get 'bundles prof))
                (packages (alist-get 'packages prof))
                (network (or (alist-get 'network prof) "deny"))
                (workspace (or (alist-get 'workspace prof) ""))
                (bare-agent (eq (alist-get 'bareAgent prof) t)))
           (unless (safeslop-profiles--confirm-create
                    existing newname agent environment bundles packages network workspace bare-agent)
             (user-error "Profile clone cancelled"))
           (safeslop-profiles-create
            newname agent environment bundles packages network workspace nil bare-agent)))))))

(defvar safeslop-profiles-mode-map
  (let ((map (make-sparse-keymap)))
    (define-key map (kbd "RET") #'safeslop-profiles-inspect)
    (define-key map (kbd "i")   #'safeslop-profiles-inspect)
    (define-key map (kbd "r")   #'safeslop-profiles-launch)
    (define-key map (kbd "e")   #'safeslop-profiles-edit)
    (define-key map (kbd "c")   #'safeslop-profiles-create)
    (define-key map (kbd "C")   #'safeslop-profiles-clone)
    (define-key map (kbd "v")   #'safeslop-profiles-validate)
    (define-key map (kbd "D")   #'safeslop-profiles-delete)
    (define-key map (kbd "g")   #'safeslop-profiles-refresh)
    (set-keymap-parent map safeslop-surface-mode-map)
    map)
  "Keymap for `safeslop-profiles-mode'.")

(define-derived-mode safeslop-profiles-mode tabulated-list-mode "safeslop-profiles"
  "Major mode for the safeslop profiles (policy) surface.
\\{safeslop-profiles-mode-map}"
  (setq tabulated-list-format
        [("Profile" 20 nil)
         ("Agent" 12 nil)
         ("Env" 11 nil)
         ("Net" 6 nil)
         ("Source" 9 nil)])
  (setq tabulated-list-padding 1)
  (tabulated-list-init-header))

;;;###autoload
(defun safeslop-profiles ()
  "Open the safeslop profiles surface: the profiles in your safeslop.cue.
Keys: RET/i inspect, r launch, e edit, c create, C clone, v validate,
D delete, g refresh; P/I/F switch surface, [/] cycle."
  (interactive)
  (let ((buf (get-buffer-create safeslop-profiles-buffer-name)))
    (with-current-buffer buf
      (unless (derived-mode-p 'safeslop-profiles-mode)
        (safeslop-profiles-mode))
      (safeslop-profiles--render))
    (pop-to-buffer-same-window buf)
    buf))

(provide 'safeslop-profiles)
;;; safeslop-profiles.el ends here
