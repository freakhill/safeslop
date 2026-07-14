;;; safeslop-portal.el --- Session dashboard for safeslop -*- lexical-binding: t; -*-

;; Copyright (C) 2026

;; Author: safeslop
;; Package-Requires: ((emacs "32.0"))
;; Keywords: tools, processes, ai

;;; Commentary:

;; The safeslop portal: one dashboard buffer listing every session with the
;; actions you take on them (open/run, reattach, status, stop, remove, new),
;; refreshable in place.  It is a thin tabulated-list view over `safeslop
;; session list --output json': rows and row actions live here, while the
;; fetch/reprint/scroll-preservation machinery is the shared
;; `safeslop-surface-render' engine (specs/0062).  Each command is the same CLI
;; the discrete `C-c s' commands run, recorded in the `*safeslop debug*' log.
;; Inspired by slopmaxx's operator console, adapted to safeslop's daemonless
;; CLI model.

;;; Code:

(require 'cl-lib)
(require 'subr-x)
(require 'tabulated-list)
(require 'iso8601)
(require 'safeslop-contract)
(require 'safeslop-client)
(require 'safeslop-surface)
(require 'safeslop-session)

(declare-function safeslop-profiles "safeslop-profiles" ())
(declare-function safeslop-profiles--render "safeslop-profiles" (&optional keep-point then))
(declare-function safeslop-profiles--goto-name "safeslop-profiles" (name))
(defvar safeslop-profiles-buffer-name)

(defconst safeslop-portal-buffer-name "*safeslop portal*"
  "Buffer name for the safeslop session dashboard.")

(defcustom safeslop-portal-refresh-interval 5
  "Seconds between automatic portal refreshes, or nil to disable.
While the portal buffer is displayed in a window, it re-fetches the session list
on this interval so status, PID, and age stay live; point is kept on the same
session across refreshes.  Press \\`g' to refresh immediately, or \\`a' to toggle
auto-refresh.  Set to nil for a static, manual-only portal."
  :type '(choice (const :tag "Disabled (manual `g' only)" nil)
                 (number :tag "Seconds"))
  :group 'safeslop)

(defvar safeslop-portal--timer nil
  "Repeating timer driving portal auto-refresh, or nil when inactive.
A single timer serves the one portal buffer (`safeslop-portal-buffer-name').")

(defvar safeslop-portal--auto-paused nil
  "Non-nil when the operator paused portal auto-refresh with `a'.")

(defvar safeslop-portal--last-refresh nil
  "Time of the last portal render, for the visible auto-refresh status line.")

(defvar-local safeslop-portal--sessions-by-id nil
  "Alist mapping session id to full session alist from the last render.")

;;; Cells -----------------------------------------------------------------

;; The env/tier/net cells are shared surface presentation (specs/0062); these
;; aliases keep the portal-era names working for tests and user config.
(defalias 'safeslop-portal--env-face #'safeslop-surface--env-face)
(defalias 'safeslop-portal--env-cell #'safeslop-surface--env-cell)
(defalias 'safeslop-portal--tier-legend #'safeslop-surface--tier-legend)
(defalias 'safeslop-portal--net-cell #'safeslop-surface--net-cell)
(defalias 'safeslop-portal--goto-id #'safeslop-surface--goto-id)

(defun safeslop-portal--field (sess key)
  "Return SESS's KEY as a display string (empty when absent)."
  (let ((v (alist-get key sess)))
    (cond ((stringp v) v)
          ((null v) "")
          (t (format "%s" v)))))

(defun safeslop-portal--short-id (id)
  "Shorten a sess-<hex> ID for the narrow Session column."
  (if (and (stringp id) (> (length id) 16))
      (concat (substring id 0 16) "…")
    (or id "")))

(defconst safeslop-portal--session-width 24
  "Display width of the Session column: short id plus optional inline name (N2).
Kept in sync with the Session entry in `tabulated-list-format'.")

(defun safeslop-portal--session-cell (sess)
  "Return the Session cell for SESS: the short id, plus its display name inline.
Per specs/0065 N2 the optional name is a suffix inside the Session column (never
an 11th column).  When a name is present the id is compressed to leave room, and
the whole cell is truncated with `truncate-string-to-width' — which counts
terminal cells, so a wide-rune name (CJK/emoji, N1) can't overflow the row."
  (let ((id (safeslop-portal--field sess 'session_id))
        (name (safeslop-portal--field sess 'name)))
    (if (string-empty-p name)
        (safeslop-portal--short-id id)
      (truncate-string-to-width
       (concat (truncate-string-to-width id 10 nil nil "…") " " name)
       safeslop-portal--session-width nil nil "…"))))

(defun safeslop-portal--status-face (status)
  "Return a face for a session STATUS string (slopmaxx-style mapping)."
  (pcase status
    ("running" 'success)
    ("created" 'warning)
    ("stopped" 'shadow)
    ((or "exited" "failed" "error" "cancelled") 'error)
    (_ 'default)))

(defun safeslop-portal--status-help (sess)
  "Return a one-line lifecycle/safety summary for SESS."
  (let ((socket (safeslop-portal--field sess 'socket))
        (revoked (eq (alist-get 'credentials_revoked sess) t))
        (err (safeslop-portal--field sess 'last_error)))
    (string-join
     (delq nil (list (safeslop-session--posture-help sess)
                     (if (and (stringp socket) (not (string-empty-p socket))) "detached" "coupled")
                     (if revoked "credentials revoked" "credentials live")
                     (unless (string-empty-p err) (concat "last error: " err))))
     " · ")))

(defun safeslop-portal--status-cell (status &optional sess)
  "Return STATUS as a tabulated-list cell coloured by its status face."
  (apply #'propertize status 'face (safeslop-portal--status-face status)
         (when sess (list 'help-echo (safeslop-portal--status-help sess)))))

(defun safeslop-portal--status-legend ()
  "Return a one-line legend for status colours."
  (concat "status: "
          (mapconcat (lambda (status)
                       (propertize status 'face (safeslop-portal--status-face status)))
                     '("running" "created" "stopped" "failed") "  ")
          "\n"))

(defun safeslop-portal--pid (sess)
  "Return SESS's pid as a display string, or an em dash when it has none."
  (let ((pid (safeslop-portal--field sess 'pid)))
    (if (string-empty-p pid) "—" pid)))

(defun safeslop-portal--humanize-age (seconds)
  "Render an age of SECONDS as a compact relative string."
  (cond ((< seconds 60) "now")
        ((< seconds 3600) (format "%dm" (floor seconds 60)))
        ((< seconds 86400) (format "%dh" (floor seconds 3600)))
        (t (format "%dd" (floor seconds 86400)))))

(defun safeslop-portal--age (sess)
  "Return how long ago SESS was last updated, compact, or an em dash."
  (let ((ts (safeslop-portal--field sess 'updated_at)))
    (if (string-empty-p ts)
        "—"
      (condition-case nil
          (safeslop-portal--humanize-age
           (float-time (time-subtract (current-time)
                                      (encode-time (iso8601-parse ts)))))
        (error "—")))))

(defun safeslop-portal--list-field (sess path)
  "Return list value from SESS at nested symbol PATH, accepting vectors too."
  (let ((value sess))
    (dolist (key path)
      (setq value (and (listp value) (alist-get key value))))
    (cond ((vectorp value) (append value nil))
          ((listp value) value)
          (t nil))))

(defun safeslop-portal--recipe-cell (sess)
  "Return SESS's resolved package identity as a compact Recipe cell."
  (let ((ids (safeslop-portal--list-field sess '(resolved identitySet))))
    (if ids
        (mapconcat #'identity ids ",")
      "—")))

(defun safeslop-portal--image-cell (sess)
  "Return SESS's recipeID/image tag as a compact Image cell."
  (let ((recipe-id (safeslop-portal--field sess 'recipeID))
        (image (safeslop-portal--field sess 'image)))
    (cond ((not (string-empty-p recipe-id)) recipe-id)
          ((string-match ":\\([^:]+\\)\\'" image) (match-string 1 image))
          (t "—"))))

;;; Credential scope (specs/0086 T2) ----------------------------------------
;; The engine emits `credential_scopes' as value-free {kind,name,scope} rows
;; (specs/0086 T1).  These pure helpers only reformat that already-safe data, so
;; the UI cannot synthesize a secret the JSON did not carry.  The field is
;; `--omitempty', so ad-hoc sessions and pre-0086 records simply lack it and
;; render an em dash.

(defconst safeslop-portal--creds-width 22
  "Display width of the compact Creds column.
Kept in sync with the Creds entry in `tabulated-list-format'.")

(defconst safeslop-portal--creds-unsafe-patterns
  '("op://" "\\benv:" "private[-_ ]?key" "begin .*key" "\\btoken\\b" "\\`[~/]")
  "Case-insensitive patterns never rendered from credential scope fields.")

(defun safeslop-portal--creds-safe-field (value)
  "Return VALUE when it is a non-empty, display-safe credential scope field.
Unsafe-looking refs, token markers, staged paths, and private-key refs are
suppressed defensively so the portal never renders them even if JSON regresses."
  (when (and (stringp value) (not (string-empty-p value)))
    (let ((case-fold-search t))
      (unless (cl-some (lambda (re) (string-match-p re value))
                       safeslop-portal--creds-unsafe-patterns)
        value))))

(defun safeslop-portal--creds-scopes (sess)
  "Return SESS's non-empty `credential_scopes' rows.
A list or vector is accepted.  Rows whose kind/name/scope all render empty are
ignored so malformed old/partial data still displays as no staged credentials."
  (cl-remove-if #'string-empty-p
                (safeslop-portal--list-field sess '(credential_scopes))
                :key #'safeslop-portal--creds-scope-string))

(defun safeslop-portal--creds-scope-string (scope)
  "Return one credential SCOPE alist as a compact \"kind name scope\" string.
SCOPE carries only the engine's non-secret kind/name/scope fields; empty fields
are dropped so a missing scope leaves no trailing whitespace."
  (string-join
   (delq nil
         (mapcar (lambda (key)
                   (safeslop-portal--creds-safe-field (alist-get key scope)))
                 '(kind name scope)))
   " "))

(defun safeslop-portal--creds-help (sess)
  "Return SESS's full value-free credential scope summary for the tooltip.
Comma-joins every scope as \"kind name scope\"; a session with no staged
credentials yields an honest note."
  (let ((scopes (safeslop-portal--creds-scopes sess)))
    (if scopes
        (mapconcat #'safeslop-portal--creds-scope-string scopes ", ")
      "no staged credentials")))

(defun safeslop-portal--creds-compact (first extra)
  "Return compact Creds text for FIRST plus EXTRA hidden scopes.
When a +N suffix is needed, keep that suffix visible inside
`safeslop-portal--creds-width'."
  (if (<= extra 0)
      (truncate-string-to-width first safeslop-portal--creds-width nil nil "…")
    (let* ((suffix (format " +%d" extra))
           (head-width (max 1 (- safeslop-portal--creds-width
                                 (string-width suffix)))))
      (concat (truncate-string-to-width first head-width nil nil "…") suffix))))

(defun safeslop-portal--creds-cell (sess)
  "Return SESS's compact Creds cell.
The cell shows the first scope plus a visible \"+N\" overflow count, with the
full comma-joined list on help-echo.  Credential-less and old records render an
em dash.  Shows only display-safe kind/name/scope, never refs, staged paths, or
key refs."
  (let ((scopes (safeslop-portal--creds-scopes sess)))
    (if (null scopes)
        "—"
      (propertize (safeslop-portal--creds-compact
                   (safeslop-portal--creds-scope-string (car scopes))
                   (1- (length scopes)))
                  'help-echo (safeslop-portal--creds-help sess)))))

(defun safeslop-portal--sessions-from (envelope)
  "Return the parsed sessions (a list of alists) from a `session list' ENVELOPE.
Pure extraction: a failed list yields nil, and the shared render engine owns
echoing the error and inserting the persistent in-buffer banner."
  (alist-get 'sessions (safeslop-contract-data envelope)))

(defconst safeslop-portal--status-ranks
  '(("running" . 0) ("created" . 1) ("stopped" . 2)
    ("exited" . 3) ("failed" . 3) ("error" . 3) ("cancelled" . 3))
  "Lifecycle sort rank per status: actionable rows first (specs/0063 F3).")

(defun safeslop-portal--status-rank (status)
  "Return STATUS's lifecycle sort rank; unknown statuses sink to the bottom."
  (or (cdr (assoc status safeslop-portal--status-ranks)) 9))

(defun safeslop-portal--session< (a b)
  "Order sessions A and B by lifecycle rank, then session id (deterministic)."
  (let ((ra (safeslop-portal--status-rank (safeslop-portal--field a 'status)))
        (rb (safeslop-portal--status-rank (safeslop-portal--field b 'status))))
    (if (= ra rb)
        (string< (safeslop-portal--field a 'session_id)
                 (safeslop-portal--field b 'session_id))
      (< ra rb))))

(defun safeslop-portal--rows (sessions)
  "Build `tabulated-list-entries' from SESSIONS (a list of alists), lifecycle-ordered.
Running and created rows come first (specs/0063 F3) so the actionable sessions
are where point lands.  Pure: SESSIONS is already-fetched data, so the row
builder never blocks on I/O."
  (mapcar
   (lambda (sess)
     (let ((id (safeslop-portal--field sess 'session_id)))
       (list id
             (vector (safeslop-portal--session-cell sess)
                     (safeslop-portal--field sess 'agent)
                     (safeslop-surface--env-cell (safeslop-portal--field sess 'environment))
                     (safeslop-surface--net-cell (safeslop-portal--field sess 'network))
                     (safeslop-portal--status-cell (safeslop-portal--field sess 'status) sess)
                     (safeslop-portal--pid sess)
                     (safeslop-portal--age sess)
                     (safeslop-portal--recipe-cell sess)
                     (safeslop-portal--image-cell sess)
                     (safeslop-portal--creds-cell sess)
                     (abbreviate-file-name (safeslop-portal--field sess 'workspace))))))
   (sort (copy-sequence sessions) #'safeslop-portal--session<)))

;;; Row actions -------------------------------------------------------------

(defun safeslop-portal--id-at-point ()
  "Return the session id on the current line, or signal a user error."
  (or (tabulated-list-get-id)
      (user-error "No session on this line")))

(defun safeslop-portal--session-at-point ()
  "Return full session data for the current row, or signal a user error."
  (let ((id (safeslop-portal--id-at-point)))
    (or (alist-get id safeslop-portal--sessions-by-id nil nil #'equal)
        (user-error "No session data for this row; press g to refresh"))))

(defun safeslop-portal--live-buffer (id)
  "Return the live terminal buffer running session ID, or nil.
Live buffers are named descriptively now (specs/0086 T3), so the id is no
longer recoverable from the buffer name: scan for the buffer-local
`safeslop-session-id' instead.  Falls back to the legacy `*safeslop-<id>*'
name so pre-0086 terminals (which have no buffer-local id) are still found."
  (or (seq-find (lambda (buf)
                  (equal id (buffer-local-value 'safeslop-session-id buf)))
                (buffer-list))
      (get-buffer (concat "*safeslop-" id "*"))))

(defun safeslop-portal--primary-action (status socket)
  "Return the obvious primary action for STATUS and SOCKET."
  (cond ((equal status "created") 'run)
        ((and (equal status "running") (stringp socket) (not (string-empty-p socket))) 'reattach)
        ((equal status "running") 'live)
        (t 'status)))

(defun safeslop-portal-open ()
  "Do the obvious action for the session at point, based on its state."
  (interactive)
  (let* ((sess (safeslop-portal--session-at-point))
         (id (safeslop-portal--field sess 'session_id)))
    (pcase (safeslop-portal--primary-action
            (safeslop-portal--field sess 'status)
            (safeslop-portal--field sess 'socket))
      ('run (when (safeslop-portal--confirm-run sess)
              (safeslop-session-attach id)))
      ('reattach (safeslop-session-reattach id))
      ('live (if-let* ((buf (safeslop-portal--live-buffer id)))
                 (pop-to-buffer buf)
               (message "safeslop: %s is already running coupled; press i for details or s to stop/revoke"
                        (safeslop-portal--short-id id))))
      ('status (safeslop-session-detail id sess)))))

(defun safeslop-portal-reattach ()
  "Reattach only when the session at point is detached and serving a socket."
  (interactive)
  (let* ((sess (safeslop-portal--session-at-point))
         (id (safeslop-portal--field sess 'session_id))
         (status (safeslop-portal--field sess 'status))
         (socket (safeslop-portal--field sess 'socket)))
    (cond
     ((and (stringp socket) (not (string-empty-p socket))) (safeslop-session-reattach id))
     ((equal status "created")
      (message "safeslop: %s is not detached — press R to start detached, or RET to run coupled"
               (safeslop-portal--short-id id)))
     ((equal status "running")
      (message "safeslop: %s is coupled, not detached — RET focuses its terminal, s stops/revokes"
               (safeslop-portal--short-id id)))
     (t (message "safeslop: %s is not running — press i for details"
                 (safeslop-portal--short-id id))))))

(defun safeslop-portal-status ()
  "Show a faced detail buffer for the session at point."
  (interactive)
  (let ((sess (safeslop-portal--session-at-point)))
    (safeslop-session-detail (safeslop-portal--field sess 'session_id) sess)))

(defun safeslop-portal--confirm-run (sess)
  "Confirm running SESS with the same isolation/network summary as Profiles.
The RET run-branch and `r' share this (specs/0063 F4): running a created
session is the same world-changing action as launching from a profile, so it
carries the same danger summary instead of starting silently."
  (yes-or-no-p
   (format "Run %s [%s]? "
           (safeslop-portal--short-id (safeslop-portal--field sess 'session_id))
           (safeslop-surface--danger-summary
            (safeslop-portal--field sess 'agent)
            (safeslop-portal--field sess 'environment)
            (safeslop-portal--field sess 'network)))))

(defun safeslop-portal-run ()
  "Run the created session at point coupled, after the isolation/network confirm.
On a session in any other state, explain the applicable key instead: RET
already does the state-aware thing."
  (interactive)
  (let* ((sess (safeslop-portal--session-at-point))
         (id (safeslop-portal--field sess 'session_id))
         (status (safeslop-portal--field sess 'status)))
    (cond
     ((equal status "created")
      (when (safeslop-portal--confirm-run sess)
        (safeslop-session-attach id)))
     ((equal status "running")
      (message "safeslop: %s is already running — RET focuses/joins it, s stops it"
               (safeslop-portal--short-id id)))
     (t (message "safeslop: %s is %s — x removes the record, c creates a new session"
                 (safeslop-portal--short-id id) status)))))

(defun safeslop-portal-run-detached ()
  "Start the session at point detached, after a credential-lifetime warning."
  (interactive)
  (let* ((sess (safeslop-portal--session-at-point))
         (id (safeslop-portal--field sess 'session_id)))
    (when (yes-or-no-p
           (format "Run %s detached? It survives this Emacs buffer and KEEPS staged credentials until stop/revoke. "
                   (safeslop-portal--short-id id)))
      (safeslop-session-run-detached id (lambda (_env) (safeslop-portal-refresh)) t))))

(defun safeslop-portal-follow-profile ()
  "Switch to Profiles and land on this session's backing profile when present."
  (interactive)
  (let ((profile (safeslop-portal--field (safeslop-portal--session-at-point) 'profile)))
    (if (string-empty-p profile)
        (message "safeslop: ad-hoc session has no backing profile; press F for Profiles")
      (safeslop-profiles)
      (when-let* ((buf (get-buffer safeslop-profiles-buffer-name)))
        (with-current-buffer buf
          (safeslop-profiles--render t (lambda () (safeslop-profiles--goto-name profile))))))))

(defun safeslop-portal-stop ()
  "Stop the session at point (revoking credentials) and refresh after completion."
  (interactive)
  (let* ((sess (safeslop-portal--session-at-point))
         (id (safeslop-portal--field sess 'session_id)))
    (when (yes-or-no-p
           (format "Stop %s? This revokes staged credentials and tears down the boundary. "
                   (safeslop-portal--short-id id)))
      (safeslop-session-stop
       id (lambda (env)
            (if (safeslop-contract-ok-p env)
                (safeslop-portal-refresh)
              (message "safeslop: stop failed: %s"
                       (safeslop-surface--error-message env "stop failed"))))
       t))))

(defun safeslop-portal-rename ()
  "Rename the session at point (set or clear its display label) and refresh.
The name is a pure label (specs/0065): a rename touches nothing derived from the
id, so it is offered in any status.  Empty input clears the name.  Refreshes the
portal in place on success rather than popping a result buffer over it."
  (interactive)
  (let* ((sess (safeslop-portal--session-at-point))
         (id (safeslop-portal--field sess 'session_id))
         (name (read-string (format "Name for %s (empty clears): "
                                    (safeslop-portal--short-id id))
                            (safeslop-portal--field sess 'name))))
    (safeslop-session-rename
     id name
     (lambda (env)
       (if (safeslop-contract-ok-p env)
           (safeslop-portal-refresh)
         (message "safeslop: rename failed: %s"
                  (safeslop-surface--error-message env "rename failed"))))
     t)))

(defun safeslop-portal-new ()
  "Create a new session.
`safeslop-session-new' is async, so it reveals the new session in the portal
itself once the create completes (via `safeslop-portal--reveal-session') — a
refresh here would race the still-running create."
  (interactive)
  (call-interactively #'safeslop-session-new))

(defun safeslop-portal-remove ()
  "Remove the stopped session at point from the list (clear a dead-session corpse).
Refuses a running session (stop it first with `s'); stopped/created records are
deleted after a light `y-or-n-p' confirm (specs/0063 F6 — cleanup of inert
records, not a lifecycle action).  `safeslop session rm' revokes any still-live
staged credentials before deleting, so a removal never orphans secrets."
  (interactive)
  (let* ((sess (safeslop-portal--session-at-point))
         (id (safeslop-portal--field sess 'session_id))
         (status (safeslop-portal--field sess 'status)))
    (when (equal status "running")
      (user-error "%s is running — press s to stop/revoke it first, then x to remove"
                  (safeslop-portal--short-id id)))
    (when (y-or-n-p (format "Remove %s (%s) from the list? "
                            (safeslop-portal--short-id id) status))
      (safeslop-session-remove
       id (lambda (env)
            (if (safeslop-contract-ok-p env)
                (safeslop-portal-refresh)
              (message "safeslop: remove failed: %s"
                       (safeslop-surface--error-message env "remove failed"))))
       t))))

(defun safeslop-portal-prune ()
  "Remove ALL stopped sessions at once (clear every dead-session corpse).
Running and created sessions are left untouched; crashed sessions (marked running
but whose process is gone) are reconciled to stopped and pruned in the same pass.
Credentials are revoked before each record is deleted."
  (interactive)
  (when (y-or-n-p "Remove all stopped sessions from the list? ")
    (safeslop-session-prune
     (lambda (env)
       (if (safeslop-contract-ok-p env)
           (let ((n (length (alist-get 'removed (safeslop-contract-data env)))))
             (message "safeslop: pruned %d stopped session%s" n (if (= n 1) "" "s"))
             (safeslop-portal-refresh))
         (message "safeslop: prune failed: %s"
                  (safeslop-surface--error-message env "prune failed"))))
     t)))

;;; Header + render -----------------------------------------------------------

(defconst safeslop-portal--key-hints
  '(("RET" . "open") ("r" . "run") ("R" . "detach") ("A" . "reattach")
    ("i" . "details") ("s" . "stop/revoke") ("x" . "remove") ("X" . "prune")
    ("c" . "new") ("N" . "rename") ("^" . "profile") ("g" . "refresh") ("a" . "auto")
    ("d" . "doctor") ("E" . "error") ("L" . "debug") ("?" . "help") ("q" . "quit"))
  "Key/action pairs shown in the portal's in-buffer shortcut legend.")

(defun safeslop-portal--legend ()
  "Return the shortcut legend line (keys faced as bindings), trailing blank line."
  (safeslop-surface--legend safeslop-portal--key-hints))

(defun safeslop-portal--auto-status-line ()
  "Return visible auto-refresh state, explicitly distinguishing UI polling from agent action."
  (let ((text (cond
               ((not (and (numberp safeslop-portal-refresh-interval)
                          (> safeslop-portal-refresh-interval 0)))
                "— auto-refresh off · g to refresh")
               (safeslop-portal--auto-paused
                "⏸ auto-refresh paused · a to resume · g to refresh")
               (t (format "⟳ auto-refresh on · every %ss%s · a to pause"
                          safeslop-portal-refresh-interval
                          (if safeslop-portal--last-refresh
                              (format " · last %s" (format-time-string "%H:%M:%S" safeslop-portal--last-refresh))
                            ""))))))
    (propertize text 'face (if safeslop-portal--auto-paused 'warning 'shadow)
                'help-echo "Polling only runs `safeslop session list`; it never runs, resumes, or advances an agent.")))

(defun safeslop-portal--header ()
  "Return the portal header block: surface tab strip, legends, auto status, shortcuts."
  (concat (safeslop-surface--tab-strip 'sessions)
          (safeslop-surface--tier-legend)
          (safeslop-portal--status-legend)
          (safeslop-surface--net-legend)
          (safeslop-portal--auto-status-line) "\n\n"
          (safeslop-portal--legend)))

(defun safeslop-portal--render (&optional keep-point after)
  "Fetch the session list and redraw the current portal buffer in place.
A thin wrapper over the shared `safeslop-surface-render' engine: this only
contributes the argv, the row builder (which also caches full session data by id
for the row actions), and the header.  KEEP-POINT and AFTER are the engine's
KEEP-POINT/THEN — see its docstring for the scroll/cursor guarantees."
  (safeslop-surface-render
   :argv '("session" "list" "--output" "json")
   :label "session list"
   :noun "sessions"
   :header-fn #'safeslop-portal--header
   :empty-fn (lambda () (safeslop-surface--empty-state "sessions" "c"))
   :entries-fn (lambda (envelope)
                 (let ((sessions (safeslop-portal--sessions-from envelope)))
                   (setq safeslop-portal--sessions-by-id
                         (mapcar (lambda (s) (cons (safeslop-portal--field s 'session_id) s))
                                 sessions))
                   (setq safeslop-portal--last-refresh (current-time))
                   (safeslop-portal--rows sessions)))
   :keep-point keep-point
   :then after))

(defun safeslop-portal--reveal-session (id)
  "If a live portal exists, refresh it and land point on session ID.
Called after `safeslop-session-new' creates ID so the new session shows up at
once — the create is async, so a plain refresh would race it."
  (let ((buf (get-buffer safeslop-portal-buffer-name)))
    (when buf
      (with-current-buffer buf
        (safeslop-portal--render t (lambda () (safeslop-surface--goto-id id)))))))

(defun safeslop-portal-refresh ()
  "Re-fetch the session list and redraw the portal, keeping point on its session."
  (interactive)
  (let ((buf (get-buffer safeslop-portal-buffer-name)))
    (when buf
      (with-current-buffer buf
        (safeslop-portal--render t)))))

;;; Auto-refresh timer ---------------------------------------------------------

(defun safeslop-portal--cancel-timer ()
  "Cancel the portal auto-refresh timer if one is running."
  (when safeslop-portal--timer
    (cancel-timer safeslop-portal--timer)
    (setq safeslop-portal--timer nil)))

(defun safeslop-portal--auto-refresh ()
  "Timer callback: refresh the portal when it is live, shown, and idle.
Self-cancels once the portal buffer is gone.  Skips a tick while any minibuffer
is active (so it never fights a `k'-stop confirmation or other prompt), while the
operator has keystrokes pending (`input-pending-p', so an automatic redraw can't
land mid-keypress and move point out from under an action key), or while a prior
async fetch for this buffer has not returned (the engine's
`safeslop-surface--refresh-in-flight', so slow `session list' calls can't stack
up).  These are the same idle guards slopmaxx's console adopted to stop refreshes
fighting operator input."
  (let ((buf (get-buffer safeslop-portal-buffer-name)))
    (cond
     ((not (buffer-live-p buf)) (safeslop-portal--cancel-timer))
     (safeslop-portal--auto-paused nil)
     ((and (get-buffer-window buf 'visible)
           (not (active-minibuffer-window))
           (not (input-pending-p))
           (not (buffer-local-value 'safeslop-surface--refresh-in-flight buf)))
      (let ((safeslop--debug-call-event 'poll))
        (safeslop-portal-refresh))))))

(defun safeslop-portal--start-timer ()
  "(Re)start the auto-refresh timer per `safeslop-portal-refresh-interval'.
A nil or non-positive interval leaves the portal static (manual `g' only)."
  (safeslop-portal--cancel-timer)
  (when (and (not safeslop-portal--auto-paused)
             (numberp safeslop-portal-refresh-interval)
             (> safeslop-portal-refresh-interval 0))
    (setq safeslop-portal--timer
          (run-at-time safeslop-portal-refresh-interval
                       safeslop-portal-refresh-interval
                       #'safeslop-portal--auto-refresh))))

(defun safeslop-portal-toggle-auto-refresh ()
  "Toggle the portal's automatic refresh on or off for this Emacs session."
  (interactive)
  (if safeslop-portal--timer
      (progn
        (setq safeslop-portal--auto-paused t)
        (safeslop-portal--cancel-timer)
        (safeslop-portal-refresh)
        (message "safeslop portal: auto-refresh paused (g to refresh, a to resume)"))
    (if (and (numberp safeslop-portal-refresh-interval)
             (> safeslop-portal-refresh-interval 0))
        (progn
          (setq safeslop-portal--auto-paused nil)
          (safeslop-portal--start-timer)
          (safeslop-portal-refresh)
          (message "safeslop portal: auto-refresh every %ss"
                   safeslop-portal-refresh-interval))
      (user-error "Set `safeslop-portal-refresh-interval' to a positive number first"))))

;;; Mode + entry point -----------------------------------------------------------

(defvar safeslop-portal-mode-map
  (let ((map (make-sparse-keymap)))
    (define-key map (kbd "RET") #'safeslop-portal-open)
    (define-key map (kbd "o")   #'safeslop-portal-open)
    (define-key map (kbd "r")   #'safeslop-portal-run)
    (define-key map (kbd "R")   #'safeslop-portal-run-detached)
    (define-key map (kbd "A")   #'safeslop-portal-reattach)
    (define-key map (kbd "i")   #'safeslop-portal-status)
    (define-key map (kbd "s")   #'safeslop-portal-stop)
    (define-key map (kbd "x")   #'safeslop-portal-remove)
    (define-key map (kbd "X")   #'safeslop-portal-prune)
    (define-key map (kbd "c")   #'safeslop-portal-new)
    (define-key map (kbd "N")   #'safeslop-portal-rename)
    (define-key map (kbd "^")   #'safeslop-portal-follow-profile)
    (define-key map (kbd "g")   #'safeslop-portal-refresh)
    (define-key map (kbd "a")   #'safeslop-portal-toggle-auto-refresh)
    ;; Inherit the shared surface switch keys (P/I/F, [/]); the portal's own keys
    ;; above take precedence, the unbound switch keys fall through to the parent.
    (set-keymap-parent map safeslop-surface-mode-map)
    map)
  "Keymap for `safeslop-portal-mode'.")

(define-derived-mode safeslop-portal-mode tabulated-list-mode "safeslop-portal"
  "Major mode for the safeslop session dashboard.
\\{safeslop-portal-mode-map}"
  ;; Columns are non-sortable so an interactive header click never re-prints and
  ;; wipes the in-buffer legend; rows are status-ordered in `safeslop-portal--rows'.
  (setq tabulated-list-format
        (vector (list "Session" safeslop-portal--session-width nil)
                '("Agent" 12 nil)
                '("Env" 10 nil)
                '("Net" 5 nil)
                '("Status" 10 nil)
                '("PID" 7 nil)
                '("Age" 6 nil)
                '("Recipe" 24 nil)
                '("Image" 13 nil)
                (list "Creds" safeslop-portal--creds-width nil)
                '("Workspace" 32 nil)))
  (setq tabulated-list-padding 1)
  (tabulated-list-init-header)
  ;; Stop the shared auto-refresh timer when the dashboard goes away.
  (add-hook 'kill-buffer-hook #'safeslop-portal--cancel-timer nil t))

;;;###autoload
(defun safeslop-portal ()
  "Open the safeslop session portal: a dashboard of sessions you can act on.
Keys: RET/o open (state-aware), r run, R run detached, A reattach, i status,
s stop, x remove, X prune, c new, N rename, ^ profile, g refresh, a toggle
auto-refresh, d doctor, L debug log, q quit.

While displayed, the portal auto-refreshes every
`safeslop-portal-refresh-interval' seconds (nil disables)."
  (interactive)
  (let ((buf (get-buffer-create safeslop-portal-buffer-name)))
    (with-current-buffer buf
      (unless (derived-mode-p 'safeslop-portal-mode)
        (safeslop-portal-mode))
      (safeslop-portal--render))
    ;; Reuse the selected window and fill it: the portal is the primary view, not a
    ;; transient popup.  Plain `pop-to-buffer' would split into a half window on
    ;; first open (the fix slopmaxx's console uses).
    (pop-to-buffer-same-window buf)
    ;; Start (or restart) the shared auto-refresh timer for the live dashboard,
    ;; unless the operator explicitly paused it with `a'.
    (safeslop-portal--start-timer)
    buf))

;;;###autoload
(defalias 'safeslop #'safeslop-portal
  "Open the safeslop session portal (alias for `safeslop-portal').")

(provide 'safeslop-portal)
;;; safeslop-portal.el ends here
