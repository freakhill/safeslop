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
;; (specs/0058 IW4) rather than handwritten snippets; and deleting is guided
;; (open the file at the block, remove it, re-validate) rather than a fragile
;; machine rewrite of the guard.  All slow calls are async (specs/0052 #7).  The
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
;;     confirm with a one-line summary, then open the file anchored at the block
;;     and fail loudly if it cannot be found.
;;   The empty state is persistent in-buffer guidance (specs/0062).
;; Navigation to a profile in the CUE file anchors to the field-opening brace
;; (`name: {'), not a loose word search that would also hit comments, string
;; values, or bundle names.

;;; Code:

(require 'subr-x)
(require 'cl-lib)
(require 'tabulated-list)
(require 'safeslop-contract)
(require 'safeslop-client)
(require 'safeslop-surface)
(require 'safeslop-output)
(require 'safeslop-session)

;; Top-level commands live in the entry file, above this layer; they are only
;; referenced late-bound (key press / explicit call after the package loads).
(declare-function safeslop-policy-check-file "safeslop" (file &optional callback))
(declare-function safeslop-doctor "safeslop" ())

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
  "Build tabulated rows from `profile list' DATA, name-ordered and safety-faced."
  (mapcar
   (lambda (entry)
     (let ((name (symbol-name (car entry)))
           (p (cdr entry)))
       (list name
             (vector name
                     (or (alist-get 'agent p) "")
                     (safeslop-surface--env-cell (or (alist-get 'environment p) ""))
                     (safeslop-surface--net-cell (or (alist-get 'network p) ""))))))
   (sort (copy-sequence (alist-get 'profiles data))
         (lambda (a b) (string< (symbol-name (car a)) (symbol-name (car b)))))))

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

(defun safeslop-profiles--row-summary (name)
  "Return a one-line \"agent, env, net\" summary for listed profile NAME, or nil."
  (when-let* ((fields (safeslop-profiles--row-fields name)))
    (format "%s, %s, %s" (nth 0 fields) (nth 1 fields) (nth 2 fields))))

;; The danger summary is shared surface presentation now (specs/0063 F4): the
;; portal's run confirm shows the same text. The alias keeps the profiles-era
;; name working for tests and user config.
(defalias 'safeslop-profiles--danger-summary #'safeslop-surface--danger-summary)

(defun safeslop-profiles--show-args (name)
  "Return argv for `profile show' of NAME, pinned to this buffer's config when known.
The list surface may be opened from one directory and revisited after
`default-directory' changes, so detail/clone operations must address the same
safeslop.cue that `profile list' loaded, not whatever the current cwd happens to
contain."
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
  "Move point to the line opening profile NAME's CUE block; return non-nil if found.
The search is scoped to the `profiles' field (including compact one-line CUE)
before matching NAME, avoiding the old loose-word failure mode that could jump to
a top-level or nested same-named block outside the profile map."
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
            (when (alist-get 'base data) (format "Base:        %s" (alist-get 'base data)))))
     "\n")))

(defun safeslop-profiles--read-name (existing &optional default)
  "Read a new profile name, validating syntax and confirming overwrite.
EXISTING is the list of names already defined; choosing one of them prompts to
confirm the create-or-update overwrite.  DEFAULT, when given, is offered as the
initial value (used by clone)."
  (let ((name nil))
    (while (not name)
      (let ((candidate (string-trim
                        (read-string
                         (if default
                             (format "Profile name (default %s): " default)
                           "Profile name: ")
                         nil nil default))))
        (cond
         ((string-empty-p candidate)
          (message "Profile name must not be empty") (sit-for 1))
         ((not (safeslop-profiles--valid-name-p candidate))
          (message "Invalid name %S: start with a letter/underscore, then letters/digits/_/-"
                   candidate)
          (sit-for 1.5))
         ((and (member candidate existing)
               (not (yes-or-no-p (format "Profile %S already exists; overwrite it? "
                                         candidate))))
          nil) ; loop and read again
         (t (setq name candidate)))))
    name))

(defun safeslop-profiles--create-summary
    (verb name agent environment bundles packages network workspace no-default-bundle)
  "Return a one-line summary for confirming a profile create/update."
  (format "%s profile `%s' (%s, %s, %s; bundles=%s; packages=%s%s%s)? "
          verb name agent environment network
          (safeslop-profiles--join bundles)
          (safeslop-profiles--join packages)
          (if (and (stringp workspace) (not (string-empty-p workspace)))
              (format "; workspace=%s" workspace)
            "")
          (if no-default-bundle "; no default agent bundle" "")))

(defun safeslop-profiles--confirm-create
    (existing name agent environment bundles packages network workspace no-default-bundle)
  "Ask for final confirmation before the async profile create/update write."
  (yes-or-no-p
   (safeslop-profiles--create-summary
    (if (member name existing) "Update" "Create")
    name agent environment bundles packages network workspace no-default-bundle)))

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

(defun safeslop-profiles-launch ()
  "Create a session from the profile at point after an explicit risk summary."
  (interactive)
  (let ((name (tabulated-list-get-id)))
    (unless name (user-error "No profile on this line"))
    (pcase-let ((`(,agent ,env ,net)
                 (or (safeslop-profiles--row-fields name) '("claude" "container" "deny"))))
      (when (yes-or-no-p
             (format "Launch session from `%s' [%s]? "
                     name (safeslop-profiles--danger-summary agent env net)))
        (safeslop-session-new-from-profile name)))))

(defun safeslop-profiles-edit ()
  "Open the active safeslop.cue for editing, jumping to the profile at point.
CUE bytes are the source of truth (specs/0029), so editing is direct; saves are
quietly re-validated."
  (interactive)
  (let ((path safeslop-profiles--config-path)
        (name (tabulated-list-get-id)))
    (unless path (user-error "No safeslop.cue known; scaffold one with `c'"))
    (safeslop-profiles--open-config path)
    (if (and name (safeslop-profiles--goto-profile-block name))
        (message "Editing `%s' in %s — saves re-validate; `C-c s F' returns to the list"
                 name (file-name-nondirectory path))
      (message "Editing %s — saves re-validate; `C-c s F' returns to the profiles list"
               path))))

;;; ---- delete --------------------------------------------------------------

(defun safeslop-profiles-delete ()
  "Guide removal of a profile from the safeslop.cue (specs/0052 D5).
Deletion is a guided manual edit, not a machine rewrite of the guard: the target
is chosen with completion (defaulting to the profile at point), confirmed with a
one-line summary, and the file is opened anchored at the profile's block.  If the
block cannot be found, this fails loudly rather than silently doing nothing.  The
save is re-validated."
  (interactive)
  (let ((path safeslop-profiles--config-path)
        (names (safeslop-profiles--names))
        (at-point (tabulated-list-get-id)))
    (unless path (user-error "No safeslop.cue known; refresh, or scaffold one with `c'"))
    (unless names (user-error "No profiles to delete"))
    (let* ((name (completing-read
                  (if at-point
                      (format "Delete profile (default %s): " at-point)
                    "Delete profile: ")
                  names nil t nil nil at-point))
           (summary (safeslop-profiles--row-summary name)))
      (when (yes-or-no-p (format "Open %s to remove profile `%s'%s? "
                                 (file-name-nondirectory path)
                                 name
                                 (if summary (format " [%s]" summary) "")))
        (safeslop-profiles--open-config path)
        (if (safeslop-profiles--goto-profile-block name)
            (message "Remove the `%s' profile block here, then save to re-validate" name)
          (user-error "Could not find the `%s' block in %s — it may already be gone; review the file"
                      name (file-name-nondirectory path)))))))

;;; ---- compose buffer ------------------------------------------------------

(defconst safeslop-profiles-compose-buffer-name "*safeslop profile compose*"
  "Buffer name for profile creation composition.")

(defvar-local safeslop-profiles-compose--state nil
  "Current profile compose state as an alist.")

(defun safeslop-profiles--alist-index (rows)
  "Return an alist mapping each row name in ROWS to its row alist."
  (mapcar (lambda (row) (cons (alist-get 'name row) row)) (append rows nil)))

(defun safeslop-profiles--catalog-indexes (bundle-data package-data)
  "Merge bundle and package catalog envelope DATA into lookup indexes."
  (list (cons 'bundles (safeslop-profiles--alist-index (alist-get 'bundles bundle-data)))
        (cons 'packages (safeslop-profiles--alist-index (alist-get 'packages package-data)))
        (cons 'defaults (alist-get 'defaults bundle-data))))

(defun safeslop-profiles--lookup-default-bundle (agent catalog)
  "Return AGENT's default bundle from CATALOG, falling back to a same-named bundle."
  (let* ((defaults (alist-get 'defaults catalog))
         (key (and agent (intern-soft agent)))
         (from-defaults (or (and key (alist-get key defaults))
                            (alist-get agent defaults nil nil #'string=))))
    (or from-defaults
        (when (assoc agent (alist-get 'bundles catalog)) agent))))

(defun safeslop-profiles--catalog-row (kind name catalog)
  "Return catalog row NAME from KIND (`bundles' or `packages') in CATALOG."
  (cdr (assoc name (alist-get kind catalog))))

(defun safeslop-profiles--row-vector (row field)
  "Return ROW FIELD as a list, accepting JSON vectors."
  (append (or (alist-get field row) []) nil))

(defun safeslop-profiles--put-package-source (rows name source locked)
  "Put package NAME in ROWS with SOURCE, preserving stronger existing locks."
  (let ((existing (assoc name rows)))
    (if existing
        (let ((cell (cdr existing)))
          (unless (alist-get 'locked cell)
            (setcdr existing (list (cons 'source source) (cons 'locked locked) (cons 'checked t))))
          rows)
      (cons (cons name (list (cons 'source source) (cons 'locked locked) (cons 'checked t))) rows))))

(defun safeslop-profiles--expand-requires (name rows catalog seen)
  "Recursively add NAME package requirements to ROWS using CATALOG, tracking SEEN."
  (if (member name seen)
      rows
    (let ((pkg (safeslop-profiles--catalog-row 'packages name catalog))
          (seen (cons name seen)))
      (dolist (req (safeslop-profiles--row-vector pkg 'requires) rows)
        (setq rows (safeslop-profiles--put-package-source rows req (format "requires:%s" name) t))
        (setq rows (safeslop-profiles--expand-requires req rows catalog seen))))))

(defun safeslop-profiles--package-rows (agent bundles packages no-default-bundle catalog)
  "Return catalog package rows for AGENT, BUNDLES, direct PACKAGES and CATALOG."
  (let ((rows nil)
        (selected-bundles (copy-sequence (or bundles nil))))
    (unless no-default-bundle
      (when-let* ((default (safeslop-profiles--lookup-default-bundle agent catalog)))
        (push (cons default 'default) selected-bundles)))
    (dolist (bundle selected-bundles)
      (let* ((name (if (consp bundle) (car bundle) bundle))
             (source-kind (if (and (consp bundle) (eq (cdr bundle) 'default)) "default" "bundle"))
             (bundle-row (safeslop-profiles--catalog-row 'bundles name catalog)))
        (dolist (pkg (safeslop-profiles--row-vector bundle-row 'packages))
          (setq rows (safeslop-profiles--put-package-source rows pkg (format "%s:%s" source-kind name) t)))))
    (dolist (pkg packages)
      (setq rows (safeslop-profiles--put-package-source rows pkg "direct" nil)))
    (dolist (pkg (mapcar #'car rows))
      (setq rows (safeslop-profiles--expand-requires pkg rows catalog nil)))
    (dolist (pkg (alist-get 'packages catalog))
      (unless (assoc (car pkg) rows)
        (push (cons (car pkg) (list (cons 'source nil) (cons 'locked nil) (cons 'checked nil))) rows)))
    (sort rows (lambda (a b) (string< (car a) (car b))))))

(defun safeslop-profiles--bundle-rows (agent bundles no-default-bundle catalog)
  "Return catalog bundle rows with selected/default lock metadata."
  (let ((default (unless no-default-bundle
                   (safeslop-profiles--lookup-default-bundle agent catalog))))
    (mapcar (lambda (bundle)
              (let* ((name (car bundle))
                     (is-default (and default (string= name default))))
                (cons name (list (cons 'checked (or is-default (member name bundles)))
                                 (cons 'locked is-default)
                                 (cons 'source (when is-default (format "default:%s" name)))))))
            (alist-get 'bundles catalog))))

(defun safeslop-profiles--bundle-suggestions (&optional directory)
  "Return suggested bundle names from local project markers in DIRECTORY."
  (let ((dir (file-name-as-directory (or directory default-directory)))
        (markers '(("go.mod" . "go")
                   ("package.json" . "web")
                   ("pyproject.toml" . "python")
                   ("Cargo.toml" . "rust"))))
    (delq nil (mapcar (lambda (m)
                        (when (file-exists-p (expand-file-name (car m) dir))
                          (cdr m)))
                      markers))))

(defun safeslop-profiles--compose-state
    (name agent environment bundles packages network workspace no-default-bundle catalog)
  "Build a pure profile compose state and derived package rows."
  (let ((suggestions (safeslop-profiles--bundle-suggestions)))
    (list (cons 'name name)
          (cons 'agent agent)
          (cons 'environment environment)
          (cons 'bundles bundles)
          (cons 'packages packages)
          (cons 'network network)
          (cons 'workspace workspace)
          (cons 'no-default-bundle no-default-bundle)
          (cons 'catalog catalog)
          (cons 'suggestions suggestions)
          (cons 'package-rows (safeslop-profiles--package-rows
                               agent bundles packages no-default-bundle catalog)))))

(defun safeslop-profiles--compose-args (state)
  "Return profile create argv for compose STATE."
  (safeslop-profiles--create-args
   (alist-get 'name state) (alist-get 'agent state) (alist-get 'environment state)
   (alist-get 'bundles state) (alist-get 'packages state)
   (alist-get 'network state) (alist-get 'workspace state)
   (alist-get 'no-default-bundle state)))

(defun safeslop-profiles--dry-run-args (state)
  "Return dry-run profile create argv for compose STATE."
  (let ((args (safeslop-profiles--compose-args state)))
    (append (butlast args 2) '("--dry-run") (last args 2))))

(defvar safeslop-profiles-compose-mode-map
  (let ((map (make-sparse-keymap)))
    (define-key map (kbd "RET") #'safeslop-profiles-compose-toggle)
    (define-key map (kbd "?") #'safeslop-profiles-compose-help)
    (define-key map (kbd "g") #'safeslop-profiles-compose-refresh)
    (define-key map (kbd "C-c C-c") #'safeslop-profiles-compose-preview-save)
    (define-key map (kbd "q") #'safeslop-profiles-compose-cancel)
    map)
  "Keymap for `safeslop-profiles-compose-mode'.")

(define-derived-mode safeslop-profiles-compose-mode special-mode "safeslop-profile-compose"
  "Major mode for composing a safeslop profile before save.")

(defun safeslop-profiles-compose--insert-row (type name checked locked source)
  "Insert one compose row and attach row metadata."
  (let ((start (point)))
    (insert (if (eq type 'bundle)
                (format "[%s] %s bundle %-18s %s\n"
                        (if checked "x" " ") (if locked "L" " ") name (or source ""))
              (format "[%s] %s %-18s package %s\n"
                      (if checked "x" " ") (if locked "L" " ") name (or source ""))))
    (put-text-property start (point) 'safeslop-row (list (cons 'type type) (cons 'name name)))))

(defun safeslop-profiles-compose--render ()
  "Render the current compose state."
  (let ((inhibit-read-only t)
        (state safeslop-profiles-compose--state))
    (erase-buffer)
    (insert "safeslop Profiles compose buffer\n")
    (insert "Keys: RET toggle unlocked, ? help, g refresh catalog, C-c C-c preview/save, q cancel\n\n")
    (insert (format "Name: %s\nAgent: %s\nEnvironment: %s\nNetwork: %s\nWorkspace: %s\n\n"
                    (alist-get 'name state) (alist-get 'agent state)
                    (alist-get 'environment state) (alist-get 'network state)
                    (or (alist-get 'workspace state) "")))
    (insert "Bundles (suggested rows are visible but not preselected):\n")
    (dolist (bundle (safeslop-profiles--bundle-rows
                     (alist-get 'agent state) (alist-get 'bundles state)
                     (alist-get 'no-default-bundle state) (alist-get 'catalog state)))
      (let* ((name (car bundle))
             (source (alist-get 'source (cdr bundle)))
             (suggested (member name (alist-get 'suggestions state))))
        (safeslop-profiles-compose--insert-row
         'bundle name (alist-get 'checked (cdr bundle)) (alist-get 'locked (cdr bundle))
         (string-join (delq nil (list source (when suggested "suggested"))) ", "))))
    (insert "\nPackages:\n")
    (dolist (pkg (alist-get 'package-rows state))
      (safeslop-profiles-compose--insert-row
       'package (car pkg) (alist-get 'checked (cdr pkg))
       (alist-get 'locked (cdr pkg)) (alist-get 'source (cdr pkg))))
    (goto-char (point-min))))

(defun safeslop-profiles-compose--row-at-point ()
  "Return compose row metadata at point."
  (or (get-text-property (point) 'safeslop-row)
      (get-text-property (max (point-min) (1- (point))) 'safeslop-row)))

(defun safeslop-profiles-compose--row-at-position (position)
  "Return compose row metadata at POSITION in the current buffer."
  (save-excursion
    (goto-char (max (point-min) (min position (point-max))))
    (safeslop-profiles-compose--row-at-point)))

(defun safeslop-profiles-compose--find-row (row)
  "Return the current position of logical compose ROW, or nil when absent."
  (when row
    (save-excursion
      (let ((position (point-min))
            found)
        (while (and (< position (point-max)) (not found))
          (when (equal row (get-text-property position 'safeslop-row))
            (setq found position))
          (setq position (next-single-property-change
                          position 'safeslop-row nil (point-max))))
        found))))

(defun safeslop-profiles-compose--capture-context ()
  "Capture logical point and scroll rows for every window showing this buffer."
  (list :point-row (safeslop-profiles-compose--row-at-point)
        :point (point)
        :views
        (mapcar
         (lambda (window)
           (list :window window
                 :point-row (safeslop-profiles-compose--row-at-position
                             (window-point window))
                 :point (window-point window)
                 :start-row (safeslop-profiles-compose--row-at-position
                             (window-start window))
                 :start (window-start window)))
         (get-buffer-window-list (current-buffer) nil t))))

(defun safeslop-profiles-compose--restore-context (context)
  "Restore logical point and scroll rows from compose CONTEXT after rendering."
  (let ((point (or (safeslop-profiles-compose--find-row
                    (plist-get context :point-row))
                   (plist-get context :point))))
    (goto-char (max (point-min) (min point (point-max)))))
  (dolist (view (plist-get context :views))
    (let ((window (plist-get view :window)))
      (when (window-live-p window)
        (let ((point (or (safeslop-profiles-compose--find-row
                          (plist-get view :point-row))
                         (plist-get view :point)))
              (start (or (safeslop-profiles-compose--find-row
                          (plist-get view :start-row))
                         (plist-get view :start))))
          (set-window-point window (max (point-min) (min point (point-max))))
          (set-window-start window (max (point-min) (min start (point-max))) t))))))

(defun safeslop-profiles-compose--render-preserving-context ()
  "Render compose state without moving an operator away from its logical row."
  (let ((context (safeslop-profiles-compose--capture-context)))
    (safeslop-profiles-compose--render)
    (safeslop-profiles-compose--restore-context context)))

(defun safeslop-profiles-compose--locked-message (name row)
  "Explain why compose ROW named NAME cannot be directly toggled."
  (message "safeslop: %s is locked because it is included by %s; toggle that source instead"
           name (or (alist-get 'source (cdr row)) "an inherited selection")))

(defun safeslop-profiles-compose-toggle ()
  "Toggle the bundle or unlocked direct package row at point."
  (interactive)
  (let* ((row (safeslop-profiles-compose--row-at-point))
         (type (alist-get 'type row))
         (name (alist-get 'name row))
         (state safeslop-profiles-compose--state)
         changed)
    (pcase type
      ('bundle
       (let ((bundle (assoc name (safeslop-profiles--bundle-rows
                                  (alist-get 'agent state) (alist-get 'bundles state)
                                  (alist-get 'no-default-bundle state) (alist-get 'catalog state)))))
         (if (alist-get 'locked (cdr bundle))
             (safeslop-profiles-compose--locked-message name bundle)
           (let ((bundles (alist-get 'bundles state)))
             (setcdr (assoc 'bundles state)
                     (if (member name bundles) (remove name bundles) (cons name bundles)))
             (setq changed t)))))
      ('package
       (let ((pkg (assoc name (alist-get 'package-rows state))))
         (if (alist-get 'locked (cdr pkg))
             (safeslop-profiles-compose--locked-message name pkg)
           (let ((packages (alist-get 'packages state)))
             (setcdr (assoc 'packages state)
                     (if (member name packages) (remove name packages) (cons name packages)))
             (setq changed t)))))
      (_ (message "safeslop: no selectable row at point")))
    (when changed
      (setcdr (assoc 'package-rows state)
              (safeslop-profiles--package-rows
               (alist-get 'agent state) (alist-get 'bundles state) (alist-get 'packages state)
               (alist-get 'no-default-bundle state) (alist-get 'catalog state)))
      (safeslop-profiles-compose--render-preserving-context))))

(defun safeslop-profiles--package-help (pkg)
  "Return help text for package catalog row PKG."
  (string-join
   (delq nil (list (format "%s (%s)" (alist-get 'name pkg) (or (alist-get 'kind pkg) "package"))
                   (when (alist-get 'version pkg) (format "version: %s" (alist-get 'version pkg)))
                   (when (alist-get 'requires pkg) (format "requires: %s" (safeslop-profiles--join (safeslop-profiles--row-vector pkg 'requires))))
                   (when (alist-get 'conflicts pkg) (format "conflicts: %s" (safeslop-profiles--join (safeslop-profiles--row-vector pkg 'conflicts))))
                   (when (alist-get 'runtimeEgress pkg) (format "runtime egress: %s" (safeslop-profiles--join (safeslop-profiles--row-vector pkg 'runtimeEgress))))
                   (when (alist-get 'note pkg) (format "note: %s" (alist-get 'note pkg)))))
   "; "))

(defun safeslop-profiles-compose-help ()
  "Show help for the bundle or package row at point."
  (interactive)
  (let* ((row (safeslop-profiles-compose--row-at-point))
         (type (alist-get 'type row))
         (name (alist-get 'name row))
         (catalog (alist-get 'catalog safeslop-profiles-compose--state)))
    (message "%s"
             (pcase type
               ('bundle (let ((bundle (safeslop-profiles--catalog-row 'bundles name catalog)))
                          (format "%s: %s; packages: %s" name
                                  (or (alist-get 'description bundle) "")
                                  (safeslop-profiles--join (safeslop-profiles--row-vector bundle 'packages)))))
               ('package (safeslop-profiles--package-help
                          (safeslop-profiles--catalog-row 'packages name catalog)))
               (_ "No row help here")))))

(defun safeslop-profiles--fetch-compose-catalog ()
  "Synchronously fetch catalog bundle/package data for compose."
  (let ((bundles (safeslop--call-json '("catalog" "list" "--bundles" "--output" "json")))
        (packages (safeslop--call-json '("catalog" "list" "--output" "json"))))
    (safeslop-profiles--catalog-indexes
     (and (safeslop-contract-ok-p bundles) (safeslop-contract-data bundles))
     (and (safeslop-contract-ok-p packages) (safeslop-contract-data packages)))))

(defun safeslop-profiles-compose-open ()
  "Open the interactive profile compose buffer."
  (interactive)
  (let ((buf (get-buffer-create safeslop-profiles-compose-buffer-name)))
    (with-current-buffer buf
      (safeslop-profiles-compose-mode)
      (setq safeslop-profiles-compose--state
            (safeslop-profiles--compose-state
             "review" "claude" "container" nil nil "deny" "." nil
             (safeslop-profiles--fetch-compose-catalog)))
      (safeslop-profiles-compose--render))
    (pop-to-buffer-same-window buf)
    buf))

(defun safeslop-profiles-compose-refresh ()
  "Refresh catalog data for the compose buffer."
  (interactive)
  (setcdr (assoc 'catalog safeslop-profiles-compose--state)
          (safeslop-profiles--fetch-compose-catalog))
  (setcdr (assoc 'package-rows safeslop-profiles-compose--state)
          (safeslop-profiles--package-rows
           (alist-get 'agent safeslop-profiles-compose--state)
           (alist-get 'bundles safeslop-profiles-compose--state)
           (alist-get 'packages safeslop-profiles-compose--state)
           (alist-get 'no-default-bundle safeslop-profiles-compose--state)
           (alist-get 'catalog safeslop-profiles-compose--state)))
  (safeslop-profiles-compose--render-preserving-context)
  (message "safeslop: catalog refreshed"))

(defun safeslop-profiles-compose-cancel ()
  "Cancel profile compose without writing."
  (interactive)
  (kill-buffer (current-buffer)))

(defun safeslop-profiles--preview-text (data)
  "Render engine-authored dry-run DATA for confirmation."
  (let* ((risk (alist-get 'risk data))
         (resolved (alist-get 'resolved data)))
    (string-join
     (delq nil
           (append (list "Engine safety preview"
                         (alist-get 'headline risk))
                   (safeslop-profiles--row-vector risk 'lines)
                   (list (format "resolved packages: %s"
                                 (safeslop-profiles--join (safeslop-profiles--row-vector resolved 'identitySet)))
                         (when (alist-get 'recipeID data) (format "recipe: %s" (alist-get 'recipeID data))))))
     "\n")))

(defun safeslop-profiles--show-preview (args env)
  "Display dry-run preview ENV for ARGS and return its text."
  (let ((text (safeslop-profiles--preview-text (safeslop-contract-data env))))
    (with-current-buffer (get-buffer-create "*safeslop profile preview*")
      (let ((inhibit-read-only t))
        (special-mode)
        (erase-buffer)
        (insert (safeslop-surface--breadcrumb args))
        (insert text)
        (insert "\n"))
      (display-buffer (current-buffer)))
    text))

(defun safeslop-profiles-compose-preview-save ()
  "Preview exact compose state with the engine, then write after explicit yes."
  (interactive)
  (let* ((state safeslop-profiles-compose--state)
         (args (safeslop-profiles--dry-run-args state)))
    (safeslop--call-json-async
     args
     (lambda (env)
       (if (not (safeslop-contract-ok-p env))
           (safeslop--show-envelope-buffer "*safeslop profile preview*" args env)
         (safeslop-profiles--show-preview args env)
         (when (yes-or-no-p "Save this profile after the engine safety preview? ")
           (safeslop-profiles-create
            (alist-get 'name state) (alist-get 'agent state) (alist-get 'environment state)
            (alist-get 'bundles state) (alist-get 'packages state)
            (alist-get 'network state) (alist-get 'workspace state) nil
            (alist-get 'no-default-bundle state))))))))

;;; ---- create / clone ------------------------------------------------------

;;;###autoload
(defun safeslop-profiles-create
    (&optional name agent environment bundles packages network workspace callback no-default-bundle)
  "Create or update a profile through `safeslop profile create'.
Interactively, prompt for NAME (validated; overwriting an existing profile is
confirmed), AGENT, ENVIRONMENT, BUNDLES, PACKAGES, NETWORK, and WORKSPACE; then
write via the CLI and refresh any live profiles surface, landing point on the new
row.  CALLBACK, when given, receives the resulting JSON contract envelope.  The
old preset scaffold is intentionally replaced by this structured flow (specs/0058
N5), while CUE remains the stored source of truth."
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
  "Clone the profile at point: prefill create from its full `profile show' data.
Only the new name is prompted (defaulting to NAME-copy); agent, environment,
network, workspace, bundles, packages, and the bare-agent opt-out are copied from
the source, so a variant is one keystroke plus a name.  The write still goes
through `profile create'."
  (interactive)
  (let ((name (tabulated-list-get-id))
        (existing (safeslop-profiles--names)))
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
         ("Net" 6 nil)])
  (setq tabulated-list-padding 1)
  (tabulated-list-init-header))

;;;###autoload
(defun safeslop-profiles ()
  "Open the safeslop profiles surface: the profiles in your safeslop.cue.
Keys: RET/i inspect, r launch, e edit, c create, C clone, v validate,
D delete (guided), g refresh; P/I/F switch surface, [/] cycle."
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
