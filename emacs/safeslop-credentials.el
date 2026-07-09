;;; safeslop-credentials.el --- Credential-posture surface for safeslop -*- lexical-binding: t; -*-

;; Copyright (C) 2026

;; Author: safeslop
;; Package-Requires: ((emacs "32.0"))
;; Keywords: tools, processes, ai

;;; Commentary:

;; The Credentials surface (specs/0067): a tabulated-list view of every
;; credential declared across the profiles in the active safeslop.cue, over
;; `safeslop creds list --output json'.  It makes a workspace's credential
;; posture legible and verifiable *before* launch — which secrets/keys a profile
;; will stage, from which source ref, whether they are ephemeral (minted per
;; session) or ref-backed, and — for the ref-backed ones — whether they resolve
;; right now.  This serves safeslop's north star: work safely with ephemeral
;; credentials and limited network/file access.
;;
;; Security posture (specs/0067): the surface is read + status + jump-to-edit; it
;; NEVER handles or shows a secret value.  The Status column comes from a
;; value-free probe in the engine (the resolved value is discarded); the Source
;; column shows the op://.../env:NAME *ref*, which is not a secret.  Authoring
;; stays CUE-canonical (specs/0029), mirroring the Profiles surface: `e' opens
;; safeslop.cue anchored at the profile's credentials block and validates on save.
;; There is no in-UI mint/revoke — ephemeral deploy keys live and die with a
;; session, owned by `run'/`session', not this view.
;;
;; Ergonomics mirror the Profiles surface:
;;   - RET / i  inspect: a read-only per-profile detail view (`creds show').
;;   - e        edit: open the CUE file at the profile's credentials block.
;;   - g        refresh: re-fetch, which re-probes readiness.
;;   All slow calls are async (specs/0052 #7) through the shared surface engine.

;;; Code:

(require 'subr-x)
(require 'cl-lib)
(require 'tabulated-list)
(require 'safeslop-contract)
(require 'safeslop-client)
(require 'safeslop-surface)
(require 'safeslop-output)
;; Reused for CUE-canonical edit navigation (open-config + profile block anchor).
(require 'safeslop-profiles)

(defconst safeslop-credentials-buffer-name "*safeslop credentials*"
  "Buffer name for the safeslop credentials surface.")

(defvar-local safeslop-credentials--config-path nil
  "Path to the safeslop.cue backing this buffer, from the last `creds list'.
The edit key acts on this file; nil until a config is found.")

(defvar-local safeslop-credentials--op nil
  "Last 1Password `op' state alist ((available . BOOL) (signedIn . BOOL)).
Set by the fetch so the header can explain op-signed-out/op-unavailable statuses
without re-probing; nil before the first fetch returns.")

(defvar-local safeslop-credentials--account-links nil
  "Value-free account-link rows from `creds status --output json'.")

(defvar-local safeslop-credentials--account-status-error nil
  "Last value-free account-link status fetch error, or nil when status is fresh.")

;;; ---- status faces + honest meaning ---------------------------------------
;; Colour reinforces the always-present status word (specs/0031): the label is
;; the signal, the face is redundant emphasis.

(defface safeslop-cred-ready '((t :inherit success))
  "Face for a `resolvable' credential: its source ref reaches now."
  :group 'safeslop)

(defface safeslop-cred-missing '((t :inherit error :weight bold))
  "Face for a `missing' credential: a ref-backed field that does not resolve."
  :group 'safeslop)

(defface safeslop-cred-attention '((t :inherit warning))
  "Face for a fixable op state (`op-signed-out'/`op-unavailable')."
  :group 'safeslop)

(defface safeslop-cred-ephemeral '((t :inherit font-lock-builtin-face))
  "Face for an `ephemeral' credential: minted per session, wiped on exit (safe)."
  :group 'safeslop)

(defface safeslop-cred-ambient '((t :inherit shadow))
  "Face for an `ambient' credential: host cloud auth (SSO/ADC), staged on launch."
  :group 'safeslop)

(defconst safeslop-credentials--status-meta
  '(("resolvable"     safeslop-cred-ready     "resolves now — the source ref is reachable (its value is never read into the UI)")
    ("missing"        safeslop-cred-missing   "unresolved — env var unset or op item not found; staging this credential will fail")
    ("op-signed-out"  safeslop-cred-attention "op:// ref but 1Password is not signed in — run `op signin`, then refresh")
    ("op-unavailable" safeslop-cred-attention "op:// ref but the `op` CLI is not installed; env: refs still resolve")
    ("ephemeral"      safeslop-cred-ephemeral "minted fresh per session and wiped on exit — no stored secret (the safe default)")
    ("ambient"        safeslop-cred-ambient   "uses host ambient cloud auth (SSO/ADC), validated when the session stages"))
  "(STATUS FACE HELP) for each value-free readiness status (specs/0067).")

(defun safeslop-credentials--status-face (status)
  "Return the face for readiness STATUS, or `default' if unknown."
  (or (nth 1 (assoc status safeslop-credentials--status-meta)) 'default))

(defun safeslop-credentials--status-help (status)
  "Return the honest help string for readiness STATUS, or nil."
  (nth 2 (assoc status safeslop-credentials--status-meta)))

(defun safeslop-credentials--status-cell (status)
  "Return STATUS as a colour-redundant cell (its meaning rides help-echo)."
  (let ((meta (assoc status safeslop-credentials--status-meta)))
    (if meta
        (propertize status 'face (nth 1 meta) 'help-echo (nth 2 meta))
      (or status ""))))

(defun safeslop-credentials--source-cell (ref scope status)
  "Return the Source cell: the REF, else the SCOPE, else a STATUS note.
REF (op://.../env:NAME) is a reference, never a value, so it is safe to show."
  (cond ((and (stringp ref) (not (string-empty-p ref))) ref)
        ((and (stringp scope) (not (string-empty-p scope))) scope)
        ((equal status "ephemeral") "(ephemeral)")
        ((equal status "ambient") "(ambient)")
        (t "")))

;;; ---- rows ----------------------------------------------------------------

(defun safeslop-credentials--row-id (profile kind name)
  "Return the stable tabulated-list id for a credential row."
  (format "%s/%s/%s" profile kind name))

(defun safeslop-credentials--rows (data)
  "Build tabulated rows from `creds list' DATA (engine-sorted by profile/kind/name)."
  (mapcar
   (lambda (r)
     (let ((profile (or (alist-get 'profile r) ""))
           (kind (or (alist-get 'kind r) ""))
           (name (or (alist-get 'name r) ""))
           (scope (or (alist-get 'scope r) ""))
           (ref (or (alist-get 'ref r) ""))
           (status (or (alist-get 'status r) "")))
       (list (safeslop-credentials--row-id profile kind name)
             (vector profile
                     kind
                     name
                     (safeslop-credentials--source-cell ref scope status)
                     (safeslop-credentials--status-cell status)))))
   (alist-get 'credentials data)))

(defun safeslop-credentials--row-profile (id)
  "Return the profile name from a row ID built by `safeslop-credentials--row-id'."
  (and (stringp id) (car (split-string id "/"))))

;;; ---- header (tab strip + status legend + op state + shortcuts) -----------

(defun safeslop-credentials--status-legend ()
  "Return a one-line legend naming the readiness statuses, each in its face."
  (concat "status: "
          (mapconcat (lambda (m) (propertize (car m) 'face (nth 1 m)))
                     safeslop-credentials--status-meta "  ")
          "\n"))

(defun safeslop-credentials--op-legend (op)
  "Return a one-line 1Password state legend for OP ((available)(signedIn)).
When OP is nil (before the first fetch) a neutral checking hint is shown."
  (concat
   "1Password: "
   (cond
    ((null op) (propertize "checking…" 'face 'safeslop-surface-hint))
    ((not (eq (alist-get 'available op) t))
     (propertize "op CLI not found" 'face 'safeslop-cred-ambient
                 'help-echo "env: refs still resolve; install `op` to resolve op:// refs"))
    ((eq (alist-get 'signedIn op) t)
     (propertize "signed in" 'face 'safeslop-cred-ready
                 'help-echo "op:// refs can be resolved"))
    (t (propertize "not signed in" 'face 'safeslop-cred-attention
                   'help-echo "run `op signin`, then refresh, to resolve op:// refs")))
   "\n"))

(defun safeslop-credentials--account-probe-cell (probe)
  "Return PROBE faced as a value-free account-link status cell."
  (propertize (or probe "")
              'face (if (equal probe "ok") 'safeslop-cred-ready 'safeslop-cred-attention)))

(defun safeslop-credentials--account-key (link)
  "Return the stable host/owner account key for LINK."
  (format "%s/%s" (or (alist-get 'host link) "") (or (alist-get 'owner link) "")))

(defun safeslop-credentials--account-line (link)
  "Return one value-free account-link status line for LINK."
  (let ((forge (or (alist-get 'forge link) ""))
        (key (safeslop-credentials--account-key link))
        (probe (safeslop-credentials--account-probe-cell (alist-get 'probe link)))
        (ttl (or (alist-get 'ttl link) "")))
    (pcase forge
      ("github"
       (format "  github  %s  app=%s inst=%s  probe=%s  ttl=%s\n"
               key (alist-get 'appID link) (alist-get 'installationID link) probe ttl))
      ("forgejo"
       (let ((port (alist-get 'sshPort link)))
         (format "  forgejo %s%s  probe=%s  ttl=%s\n"
                 key (if port (format " ssh-port=%s" port) "") probe ttl)))
      (_ (format "  %s  %s  probe=%s  ttl=%s\n" forge key probe ttl)))))

(defun safeslop-credentials--account-section ()
  "Return the value-free linked-account status section for the header."
  (cond
   (safeslop-credentials--account-status-error
    (concat "account links: unavailable — "
            (propertize safeslop-credentials--account-status-error 'face 'safeslop-cred-attention)
            "\n\n"))
   (safeslop-credentials--account-links
    (concat "account links:\n"
            (mapconcat #'safeslop-credentials--account-line
                       safeslop-credentials--account-links "")
            "\n"))
   (t
    (concat "account links: none — "
            (propertize "a" 'face 'help-key-binding) " link account, "
            (propertize "u" 'face 'help-key-binding) " unlink\n\n"))))

(defconst safeslop-credentials--key-hints
  '(("RET" . "inspect") ("a" . "link account") ("u" . "unlink account")
    ("p" . "pick repos") ("e" . "edit") ("g" . "refresh")
    ("d" . "doctor") ("E" . "error") ("L" . "debug") ("?" . "help") ("q" . "quit"))
  "Key/action pairs shown in the credentials surface's in-buffer legend.")

(defun safeslop-credentials--header ()
  "Return the credentials header block: tab strip, status + op/account legends, shortcuts."
  (concat (safeslop-surface--tab-strip 'credentials)
          (safeslop-credentials--status-legend)
          (safeslop-credentials--op-legend safeslop-credentials--op)
          (safeslop-credentials--account-section)
          (safeslop-surface--legend safeslop-credentials--key-hints)))

(defun safeslop-credentials--empty-state (&optional config-path)
  "Return persistent guidance for an empty (but successful) credentials listing."
  (if config-path
      (concat (propertize (format "No credentials declared in %s"
                                  (abbreviate-file-name config-path))
                          'face 'safeslop-surface-hint)
              " — add `secrets'/`credentials' to a profile ("
              (propertize "e" 'face 'help-key-binding) " opens the file), or "
              (propertize "g" 'face 'help-key-binding) " to refresh.\n")
    (concat (propertize "No safeslop.cue found" 'face 'safeslop-surface-hint)
            " — open one via the Profiles surface ("
            (propertize "F" 'face 'help-key-binding) "), or "
            (propertize "g" 'face 'help-key-binding) " to retry.\n")))

;;; ---- render --------------------------------------------------------------

(defun safeslop-credentials--record-account-status (envelope)
  "Record account-link status ENVELOPE for the next header render.
Failures are deliberately localized to the account-link section; they must not
hide the existing `creds list' rows."
  (condition-case err
      (if (safeslop-contract-ok-p envelope)
          (setq safeslop-credentials--account-links
                (safeslop-contract-creds-status-links envelope)
                safeslop-credentials--account-status-error nil)
        (setq safeslop-credentials--account-links nil
              safeslop-credentials--account-status-error
              (safeslop-surface--error-message envelope "creds status failed")))
    (error
     (setq safeslop-credentials--account-links nil
           safeslop-credentials--account-status-error (error-message-string err)))))

(defun safeslop-credentials--render-list (&optional keep-point then)
  "Fetch `creds list' and redraw the current surface buffer in place."
  (safeslop-surface-render
   :argv '("creds" "list" "--output" "json")
   :label "creds list"
   :noun "credentials"
   :header-fn #'safeslop-credentials--header
   :empty-fn (lambda () (safeslop-credentials--empty-state safeslop-credentials--config-path))
   :entries-fn (lambda (envelope)
                 (if (safeslop-contract-ok-p envelope)
                     (let ((data (safeslop-contract-data envelope)))
                       (setq safeslop-credentials--config-path (alist-get 'config data)
                             safeslop-credentials--op (alist-get 'op data))
                       (safeslop-credentials--rows data))
                   (setq safeslop-credentials--config-path nil
                         safeslop-credentials--op nil)
                   nil))
   :keep-point keep-point
   :then then))

(defun safeslop-credentials--render (&optional keep-point then)
  "Fetch account links and `creds list', then redraw the surface.
The account-link fetch is best-effort and value-free (`creds status --output
json'); a failure degrades only the account-link section and still proceeds to
the credential posture table.  KEEP-POINT/THEN are the surface engine's."
  (let ((buf (current-buffer)))
    (safeslop--call-json-async
     '("creds" "status" "--output" "json")
     (lambda (envelope)
       (when (buffer-live-p buf)
         (with-current-buffer buf
           (safeslop-credentials--record-account-status envelope)
           (safeslop-credentials--render-list keep-point then)))))))

(defun safeslop-credentials-refresh ()
  "Re-fetch the credential posture (re-probing readiness), keeping point on its row."
  (interactive)
  (safeslop-credentials--render t))

;;; ---- inspect (read-only per-profile detail) ------------------------------

(defvar-local safeslop-credentials--inspect-profile nil
  "Profile described by the current credentials inspect buffer.")

(defvar-local safeslop-credentials--inspect-args nil
  "Exact `creds show' argv behind this inspect buffer, for `g' refresh.")

(defun safeslop-credentials--show-args (profile)
  "Return argv for `creds show' of PROFILE, pinned to this buffer's config path."
  (append (list "creds" "show" profile)
          (when safeslop-credentials--config-path (list safeslop-credentials--config-path))
          (list "--output" "json")))

(defun safeslop-credentials--inspect-row (r)
  "Format one `creds show' credential alist R as a detail block."
  (let ((kind (or (alist-get 'kind r) ""))
        (name (or (alist-get 'name r) ""))
        (scope (or (alist-get 'scope r) ""))
        (ref (or (alist-get 'ref r) ""))
        (status (or (alist-get 'status r) "")))
    (concat
     (format "  %-8s %s%s\n" kind name
             (if (string-empty-p scope) "" (format "  [%s]" scope)))
     (format "           source: %s\n"
             (if (string-empty-p ref) "(none — ephemeral/ambient)" ref))
     (format "           status: %s — %s"
             (safeslop-credentials--status-cell status)
             (or (safeslop-credentials--status-help status) "")))))

(defun safeslop-credentials--inspect-format (data)
  "Format `creds show' DATA as a human-readable, faced inspection string."
  (let ((profile (or (alist-get 'profile data) ""))
        (op (alist-get 'op data))
        (rows (alist-get 'credentials data)))
    (concat
     (format "Profile:   %s\n" profile)
     (safeslop-credentials--op-legend op)
     (if rows
         (mapconcat #'safeslop-credentials--inspect-row rows "\n")
       (propertize "No credentials declared for this profile." 'face 'safeslop-surface-hint))
     "\n")))

(defvar safeslop-credentials-inspect-mode-map
  (let ((map (make-sparse-keymap)))
    (define-key map (kbd "e") #'safeslop-credentials-inspect-edit)
    (define-key map (kbd "g") #'safeslop-credentials-inspect-refresh)
    (define-key map (kbd "RET") #'safeslop-credentials-inspect-back)
    (define-key map (kbd "q") #'quit-window)
    map)
  "Keymap for credentials inspect buffers (composed over `safeslop-output-mode-map').")

(defun safeslop-credentials--inspect-legend ()
  "Return credentials inspect key help."
  (concat (propertize "e" 'face 'help-key-binding) " edit  "
          (propertize "g" 'face 'help-key-binding) " refresh  "
          (propertize "RET" 'face 'help-key-binding) " back  "
          (propertize "q" 'face 'help-key-binding) " quit\n\n"))

(defun safeslop-credentials--from-inspect (command)
  "Return to the credentials list on this inspect buffer's profile, run COMMAND."
  (let ((profile safeslop-credentials--inspect-profile)
        (buf (get-buffer safeslop-credentials-buffer-name)))
    (unless (buffer-live-p buf)
      (user-error "The credentials list is gone; press `C-c s K' to reopen it"))
    (pop-to-buffer-same-window buf)
    (when profile
      (safeslop-surface--goto-id (safeslop-credentials--first-row-of-profile profile)))
    (when command (call-interactively command))))

(defun safeslop-credentials--first-row-of-profile (profile)
  "Return the id of the first listed row for PROFILE, or nil."
  (seq-find (lambda (id) (equal (safeslop-credentials--row-profile id) profile))
            (mapcar #'car tabulated-list-entries)))

(defun safeslop-credentials-inspect-edit ()
  "From an inspect buffer, jump to the list row and open the CUE file to edit."
  (interactive)
  (safeslop-credentials--from-inspect #'safeslop-credentials-edit))

(defun safeslop-credentials-inspect-back ()
  "Return from an inspect buffer to the credentials list row."
  (interactive)
  (safeslop-credentials--from-inspect nil))

(defun safeslop-credentials-inspect-refresh ()
  "Re-fetch this buffer's `creds show' and re-render the faced inspect view."
  (interactive)
  (let ((profile safeslop-credentials--inspect-profile)
        (args safeslop-credentials--inspect-args))
    (safeslop--call-json-async
     args
     (lambda (env)
       (if (safeslop-contract-ok-p env)
           (safeslop-credentials--show-inspect profile (safeslop-contract-data env) args)
         (message "safeslop: creds show failed: %s"
                  (safeslop-surface--error-message env "creds show failed")))))))

(defun safeslop-credentials--show-inspect (profile data &optional args)
  "Render `creds show' DATA for PROFILE in a read-only actionable detail buffer."
  (let ((buf (get-buffer-create (format "*safeslop creds %s*" profile))))
    (with-current-buffer buf
      (safeslop-output-mode)
      (setq safeslop-credentials--inspect-profile profile
            safeslop-credentials--inspect-args args)
      ;; Feed the shared output refresh (`g'/Evil `gr') the faced re-render.
      (setq safeslop-output--args args
            safeslop-output--buffer-name (buffer-name)
            safeslop-output--rerender
            (lambda (env)
              (safeslop-credentials--show-inspect profile (safeslop-contract-data env) args)))
      (use-local-map (make-composed-keymap safeslop-credentials-inspect-mode-map
                                           safeslop-output-mode-map))
      (let ((inhibit-read-only t))
        (erase-buffer)
        (insert (safeslop-surface--tab-strip 'credentials))
        (insert (format "▸ creds show %s\n\n" profile))
        (insert (safeslop-credentials--inspect-legend))
        (insert (safeslop-credentials--inspect-format data))
        (goto-char (point-min))))
    (pop-to-buffer buf)
    buf))

(defun safeslop-credentials-inspect ()
  "Show the credential posture of the profile at point in a read-only buffer (RET).
Renders `creds show' — each declared credential with its kind, name, scope,
source ref, and value-free readiness status — without touching the CUE file or
any secret value."
  (interactive)
  (let* ((id (tabulated-list-get-id))
         (profile (safeslop-credentials--row-profile id)))
    (unless profile (user-error "No credential on this line"))
    (let ((args (safeslop-credentials--show-args profile)))
      (safeslop--call-json-async
       args
       (lambda (env)
         (if (safeslop-contract-ok-p env)
             (safeslop-credentials--show-inspect profile (safeslop-contract-data env) args)
           (message "safeslop: creds show failed: %s"
                    (safeslop-surface--error-message env "creds show failed"))))))))

;;; ---- edit (CUE-canonical, jump to the credentials block) -----------------

(defun safeslop-credentials--goto-credentials-field ()
  "With point on a profile block opener, move to its `credentials'/`secrets' field.
Bounded to a conservative window so a match in a later profile is never chosen; if
the profile declares neither field, point stays on the block opener.  Returns
non-nil when a field was found."
  (let ((limit (min (point-max) (+ (point) 4000))))
    (re-search-forward "^[ \t]*\"?\\(?:credentials\\|secrets\\)\"?[ \t]*:" limit t)))

(defun safeslop-credentials-edit ()
  "Open the active safeslop.cue for editing, jumping to the profile's creds block.
CUE bytes are the source of truth (specs/0029), so editing is direct; saves are
quietly re-validated.  Secrets/tokens are edited as op://.../env: refs here — the
value stays in 1Password or the environment, never in the config or this UI."
  (interactive)
  (let* ((path safeslop-credentials--config-path)
         (id (tabulated-list-get-id))
         (profile (safeslop-credentials--row-profile id)))
    (unless path (user-error "No safeslop.cue known; refresh with `g' or open one via `F'"))
    (safeslop-profiles--open-config path)
    (if (and profile (safeslop-profiles--goto-profile-block profile))
        (progn
          (safeslop-credentials--goto-credentials-field)
          (message "Editing `%s' credentials in %s — saves re-validate; `C-c s K' returns to the list"
                   profile (file-name-nondirectory path)))
      (message "Editing %s — saves re-validate; `C-c s K' returns to the credentials list"
               path))))

;;; ---- account link/unlink actions ----------------------------------------

(defun safeslop-credentials--raw-ok-envelope (stdout)
  "Build a small ok envelope around raw command STDOUT."
  (list (cons 'schema_version safeslop-contract-schema-version)
        (cons 'ok t)
        (cons 'data (list (cons 'message (string-trim (or stdout "")))))
        (cons 'warnings nil)
        (cons 'errors nil)))

(defun safeslop-credentials--raw-error-envelope (message)
  "Build a small error envelope for raw command MESSAGE."
  (list (cons 'schema_version safeslop-contract-schema-version)
        (cons 'ok :json-false)
        (cons 'data nil)
        (cons 'warnings nil)
        (cons 'errors (list (list (cons 'code "IO_ERROR")
                                  (cons 'message message)
                                  (cons 'details nil)
                                  (cons 'retryable :json-false))))))

(defun safeslop-credentials--call-raw-async (args callback)
  "Run safeslop ARGS asynchronously and wrap raw stdout into a result envelope.
Used for existing `creds link|unlink' verbs, whose CLI output is human text.
ARGS are passed as argv (no shell) and contain refs/ids only, never secret
values.  CALLBACK receives a contract-shaped envelope."
  (safeslop--debug :event 'call :argv (string-join args " "))
  (let* ((out-buf (generate-new-buffer " *safeslop-creds-raw*"))
         (err-buf (generate-new-buffer " *safeslop-creds-raw-stderr*")))
    (condition-case err
        (let ((proc
               (make-process
                :name "safeslop-creds-raw"
                :buffer out-buf
                :command (cons safeslop-program args)
                :connection-type 'pipe
                :noquery t
                :stderr err-buf
                :sentinel
                (lambda (proc _event)
                  (unless (process-live-p proc)
                    (let* ((status (process-exit-status proc))
                           (stdout (when (buffer-live-p out-buf)
                                     (with-current-buffer out-buf (buffer-string))))
                           (stderr (when (buffer-live-p err-buf)
                                     (with-current-buffer err-buf (string-trim (buffer-string))))))
                      (when (buffer-live-p out-buf) (kill-buffer out-buf))
                      (when (buffer-live-p err-buf) (kill-buffer err-buf))
                      (funcall callback
                               (if (equal status 0)
                                   (safeslop-credentials--raw-ok-envelope stdout)
                                 (safeslop-credentials--raw-error-envelope
                                  (if (and stderr (not (string-empty-p stderr)))
                                      stderr
                                    (format "safeslop exited with status %s" status)))))))))))
          (set-process-query-on-exit-flag proc nil)
          proc)
      (error
       (when (buffer-live-p out-buf) (kill-buffer out-buf))
       (when (buffer-live-p err-buf) (kill-buffer err-buf))
       (funcall callback (safeslop-credentials--raw-error-envelope (error-message-string err)))
       nil))))

(defun safeslop-credentials--nonempty-read-string (prompt &optional default)
  "Read a non-empty string with PROMPT and optional DEFAULT."
  (let ((value (read-string prompt nil nil default)))
    (when (string-empty-p value)
      (user-error "%s is required" (string-trim-right prompt "[: ]+")))
    value))

(defun safeslop-credentials--link-github-args (host app-id installation-id key-ref)
  "Return exact argv for linking a GitHub App account."
  (list "creds" "link" "github"
        "--host" host
        "--app-id" (number-to-string app-id)
        "--installation-id" (number-to-string installation-id)
        "--key-ref" key-ref))

(defun safeslop-credentials--link-forgejo-args (host owner token-ref ssh-port)
  "Return exact argv for linking a Forgejo account."
  (append (list "creds" "link" "forgejo"
                "--host" host
                "--owner" owner
                "--token-ref" token-ref)
          (when (and ssh-port (not (string-empty-p ssh-port)))
            (list "--ssh-port" ssh-port))))

(defun safeslop-credentials--run-account-mutation (buffer-name args)
  "Run mutating account ARGS, show a result buffer, and refresh this surface on ok."
  (let ((source (current-buffer)))
    (safeslop-credentials--call-raw-async
     args
     (lambda (env)
       (when (safeslop-contract-ok-p env)
         (if (and (buffer-live-p source)
                  (with-current-buffer source (derived-mode-p 'safeslop-credentials-mode)))
             (with-current-buffer source (safeslop-credentials-refresh))
           (safeslop-credentials-refresh)))
       (safeslop--show-envelope-buffer buffer-name args env)))))

(defun safeslop-credentials-link-account ()
  "Prompt for non-secret forge link refs/ids and run `safeslop creds link'."
  (interactive)
  (let ((provider (completing-read "Link account provider: " '("github" "forgejo") nil t)))
    (pcase provider
      ("github"
       (let* ((host (safeslop-credentials--nonempty-read-string "GitHub host: " "github.com"))
              (app-id (read-number "GitHub App id: "))
              (installation-id (read-number "GitHub installation id: "))
              (key-ref (safeslop-credentials--nonempty-read-string "App private key ref (op:// or env:): "))
              (args (safeslop-credentials--link-github-args host app-id installation-id key-ref)))
         (safeslop-credentials--run-account-mutation "*safeslop creds link*" args)))
      ("forgejo"
       (let* ((host (safeslop-credentials--nonempty-read-string "Forgejo host: "))
              (owner (safeslop-credentials--nonempty-read-string "Forgejo owner/login: "))
              (token-ref (safeslop-credentials--nonempty-read-string "Forgejo token ref (op:// or env:): "))
              (ssh-port (read-string "Forgejo SSH port (blank for default): "))
              (args (safeslop-credentials--link-forgejo-args host owner token-ref ssh-port)))
         (safeslop-credentials--run-account-mutation "*safeslop creds link*" args)))
      (_ (user-error "Unknown provider %s" provider)))))

(defun safeslop-credentials-unlink-account ()
  "Choose a linked account and run `safeslop creds unlink'."
  (interactive)
  (unless safeslop-credentials--account-links
    (user-error "No account links are loaded; press `a' to link one or `g' to refresh"))
  (let* ((keys (delete-dups (mapcar #'safeslop-credentials--account-key
                                    safeslop-credentials--account-links)))
         (key (completing-read "Unlink account: " keys nil t)))
    (when (yes-or-no-p (format "Unlink %s? " key))
      (safeslop-credentials--run-account-mutation
       "*safeslop creds unlink*"
       (list "creds" "unlink" key)))))

;;; ---- repository/scope picker --------------------------------------------

(defun safeslop-credentials--repeat-flags (flag values)
  "Return repeated FLAG argv entries for VALUES."
  (apply #'append (mapcar (lambda (v) (list flag v)) values)))

(defun safeslop-credentials--profile-credentials-args
    (profile config-path provider use-origin read-repos write-repos url ssh-port)
  "Return exact argv for `profile credentials set' from picker fields."
  (append (list "profile" "credentials" "set" profile)
          (when (and config-path (not (string-empty-p config-path)))
            (list config-path))
          (list "--provider" provider)
          (when (and (equal provider "forgejo") url (not (string-empty-p url)))
            (list "--url" url))
          (when (and (equal provider "forgejo") ssh-port (not (string-empty-p ssh-port)))
            (list "--ssh-port" ssh-port))
          (if use-origin
              (list "--use-origin")
            (append (safeslop-credentials--repeat-flags "--repo" read-repos)
                    (safeslop-credentials--repeat-flags "--write-repo" write-repos)))
          (list "--output" "json")))

(defun safeslop-credentials--profile-candidates ()
  "Return profile candidates visible in the credentials table."
  (delete-dups
   (delq nil (mapcar (lambda (entry) (safeslop-credentials--row-profile (car entry)))
                     tabulated-list-entries))))

(defun safeslop-credentials--split-repo-list (text)
  "Split comma/space separated repo TEXT into a list, dropping blanks."
  (split-string (or text "") "[ ,\t\n]+" t))

(defun safeslop-credentials--profile-credentials-summary
    (profile provider use-origin read-repos write-repos url ssh-port)
  "Return the value-free confirmation summary for a profile credential write."
  (concat
   (format "Profile: %s\nProvider: %s\n" profile provider)
   (if use-origin
       "Repositories: origin inference (resolved at stage time)\n"
     (concat
      (format "Read-only repos: %s\n" (if read-repos (string-join read-repos ", ") "(none)"))
      (if write-repos
          (propertize (format "WRITE: %s\n" (string-join write-repos ", "))
                      'face 'safeslop-cred-attention)
        "WRITE: (none)\n")))
   (when (equal provider "forgejo")
     (format "Forgejo URL: %s%s\n" (or url "(origin inferred)")
             (if (and ssh-port (not (string-empty-p ssh-port)))
                 (format "  ssh-port=%s" ssh-port)
               "")))))

(defun safeslop-credentials--refresh-after-profile-credential-save (source)
  "Refresh Credentials SOURCE and the Profiles buffer, if present."
  (when (and (buffer-live-p source)
             (with-current-buffer source (derived-mode-p 'safeslop-credentials-mode)))
    (with-current-buffer source (safeslop-credentials-refresh)))
  (when-let* ((profiles (get-buffer safeslop-profiles-buffer-name)))
    (with-current-buffer profiles
      (when (derived-mode-p 'safeslop-profiles-mode)
        (safeslop-profiles-refresh)))))

(defun safeslop-credentials-pick-repositories ()
  "Pick repo scopes for a profile and call `profile credentials set'."
  (interactive)
  (let* ((source (current-buffer))
         (profiles (safeslop-credentials--profile-candidates))
         (default-profile (safeslop-credentials--row-profile (tabulated-list-get-id)))
         (profile (completing-read "Profile: " profiles nil nil nil nil default-profile))
         (provider (completing-read "Forge provider: " '("github" "forgejo") nil t nil nil "github"))
         (mode (completing-read "Repository mode: " '("origin inference" "explicit repos") nil t nil nil "origin inference"))
         (use-origin (equal mode "origin inference"))
         (read-repos nil)
         (write-repos nil)
         (url nil)
         (ssh-port nil))
    (unless use-origin
      (setq read-repos (safeslop-credentials--split-repo-list
                        (read-string "Read-only repos (owner/name, comma-separated): "))
            write-repos (safeslop-credentials--split-repo-list
                         (read-string "Write repos (owner/name, comma-separated): ")))
      (when (and (null read-repos) (null write-repos))
        (user-error "At least one read or write repo is required for explicit mode")))
    (when (and (equal provider "forgejo") (not use-origin))
      (setq url (safeslop-credentials--nonempty-read-string "Forgejo URL: ")
            ssh-port (read-string "Forgejo SSH port (blank for default): ")))
    (let* ((summary (safeslop-credentials--profile-credentials-summary
                     profile provider use-origin read-repos write-repos url ssh-port))
           (args (safeslop-credentials--profile-credentials-args
                  profile safeslop-credentials--config-path provider use-origin
                  read-repos write-repos url ssh-port)))
      (if (yes-or-no-p (concat "Save profile credential scopes?\n" summary))
          (safeslop--call-json-async
           args
           (lambda (env)
             (if (safeslop-contract-ok-p env)
                 (progn
                   (message "safeslop: profile credentials updated for %s" profile)
                   (safeslop-credentials--refresh-after-profile-credential-save source))
               (safeslop--show-envelope-buffer "*safeslop profile credentials*" args env))))
        (message "safeslop: profile credential update cancelled")))))

;;; ---- mode + entry --------------------------------------------------------

(defvar safeslop-credentials-mode-map
  (let ((map (make-sparse-keymap)))
    (define-key map (kbd "RET") #'safeslop-credentials-inspect)
    (define-key map (kbd "i")   #'safeslop-credentials-inspect)
    (define-key map (kbd "e")   #'safeslop-credentials-edit)
    (define-key map (kbd "g")   #'safeslop-credentials-refresh)
    (define-key map (kbd "a")   #'safeslop-credentials-link-account)
    (define-key map (kbd "u")   #'safeslop-credentials-unlink-account)
    (define-key map (kbd "p")   #'safeslop-credentials-pick-repositories)
    (set-keymap-parent map safeslop-surface-mode-map)
    map)
  "Keymap for `safeslop-credentials-mode'.")

(define-derived-mode safeslop-credentials-mode tabulated-list-mode "safeslop-credentials"
  "Major mode for the safeslop credentials (posture) surface.
\\{safeslop-credentials-mode-map}"
  (setq tabulated-list-format
        [("Profile" 12 t)
         ("Kind" 8 t)
         ("Name" 22 t)
         ("Source" 26 nil)
         ("Status" 14 nil)])
  (setq tabulated-list-padding 1)
  (tabulated-list-init-header))

;;;###autoload
(defun safeslop-credentials ()
  "Open the safeslop credentials surface: the credential posture of your safeslop.cue.
For every profile, shows which secrets/keys it stages, from which source ref,
whether they are ephemeral (minted per session) or ref-backed, and — for the
ref-backed ones — whether they resolve now.  No secret value is ever shown.
Keys: RET/i inspect, e edit, g refresh; P/F/K switch surface, [/] cycle."
  (interactive)
  (let ((buf (get-buffer-create safeslop-credentials-buffer-name)))
    (with-current-buffer buf
      (unless (derived-mode-p 'safeslop-credentials-mode)
        (safeslop-credentials-mode))
      (safeslop-credentials--render))
    (pop-to-buffer-same-window buf)
    buf))

(provide 'safeslop-credentials)
;;; safeslop-credentials.el ends here
