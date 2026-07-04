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
   "\n\n"))

(defconst safeslop-credentials--key-hints
  '(("RET" . "inspect") ("e" . "edit") ("g" . "refresh")
    ("d" . "doctor") ("E" . "error") ("L" . "debug") ("?" . "help") ("q" . "quit"))
  "Key/action pairs shown in the credentials surface's in-buffer legend.")

(defun safeslop-credentials--header ()
  "Return the credentials header block: tab strip, status + op legends, shortcuts."
  (concat (safeslop-surface--tab-strip 'credentials)
          (safeslop-credentials--status-legend)
          (safeslop-credentials--op-legend safeslop-credentials--op)
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

(defun safeslop-credentials--render (&optional keep-point then)
  "Fetch `creds list' and redraw the current surface buffer in place.
Thin wrapper over the shared `safeslop-surface-render' engine (async, never
freezes Emacs): contributes the argv, the row builder (which also records the
backing safeslop.cue path and op state for the header/edit), the header, and the
empty state.  KEEP-POINT/THEN are the engine's."
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

;;; ---- mode + entry --------------------------------------------------------

(defvar safeslop-credentials-mode-map
  (let ((map (make-sparse-keymap)))
    (define-key map (kbd "RET") #'safeslop-credentials-inspect)
    (define-key map (kbd "i")   #'safeslop-credentials-inspect)
    (define-key map (kbd "e")   #'safeslop-credentials-edit)
    (define-key map (kbd "g")   #'safeslop-credentials-refresh)
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
