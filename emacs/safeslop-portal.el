;;; safeslop-portal.el --- Session dashboard for safeslop -*- lexical-binding: t; -*-

;; Copyright (C) 2026

;; Author: safeslop
;; Package-Requires: ((emacs "32.0"))
;; Keywords: tools, processes, ai

;;; Commentary:

;; The safeslop portal: one dashboard buffer listing every session with the
;; actions you take on them (open/run, reattach, status, stop, new), refreshable
;; in place.  It is a thin tabulated-list view over `safeslop session list
;; --output json'; each command is the same CLI the discrete `C-c s' commands run,
;; recorded in the `*safeslop debug*' log.  Inspired by slopmaxx's operator
;; console, adapted to safeslop's daemonless CLI model.

;;; Code:

(require 'subr-x)
(require 'tabulated-list)
(require 'iso8601)
(require 'safeslop-contract)
(require 'safeslop-surface)

(defvar safeslop-program)
(declare-function safeslop--call-json "safeslop" (args))
(declare-function safeslop--call-json-async "safeslop" (args callback))
(declare-function safeslop-doctor "safeslop" ())
(declare-function safeslop-debug-log "safeslop" ())
(declare-function safeslop-session-new "safeslop-session" (&optional agent workspace))
(declare-function safeslop-session-attach "safeslop-session" (&optional session-id))
(declare-function safeslop-session-reattach "safeslop-session" (&optional session-id))
(declare-function safeslop-session-status "safeslop-session" (&optional session-id))
(declare-function safeslop-session-stop "safeslop-session" (&optional session-id))

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

(defun safeslop-portal--status-face (status)
  "Return a face for a session STATUS string (slopmaxx-style mapping)."
  (pcase status
    ("running" 'success)
    ("created" 'warning)
    ("stopped" 'shadow)
    ((or "exited" "failed" "error" "cancelled") 'error)
    (_ 'default)))

(defun safeslop-portal--status-cell (status)
  "Return STATUS as a tabulated-list cell coloured by its status face."
  (propertize status 'face (safeslop-portal--status-face status)))

;; --- Isolation-tier signalling (specs/0052 #5) -------------------------------
;; The Env column already shows the environment name (host/container/vm); we
;; colour that text by isolation strength so the honest danger ramp the old GUI
;; drew as chrome is back — colour reinforces the always-present word, it never
;; replaces it (specs/0031 non-colour danger channel).

(defface safeslop-tier-host '((t :inherit error))
  "Face for the `host' environment: no isolation boundary (most dangerous)."
  :group 'safeslop)
(defface safeslop-tier-container '((t :inherit success))
  "Face for the `container' environment: egress-allowlisted network control."
  :group 'safeslop)
(defconst safeslop-portal--env-tiers
  ;; Mirrors internal/engine/policy/policy.go EnvTier (tier label + honest note),
  ;; ordered host < container (least -> most isolated).  Keep in sync with
  ;; EnvTier; doctor's data.tiers carries the authoritative copy at runtime.
  '(("host"      safeslop-tier-host      "none"               "no isolation boundary — the agent runs as you, with your full account")
    ("container" safeslop-tier-container "egress-allowlisted" "container + default-deny per-domain egress allowlist: stops curl|sh + accidental beaconing, not exfil via an allowed domain"))
  "Per-environment (FACE TIER NOTE) used to colour and annotate the Env cell.")

(defun safeslop-portal--env-face (env)
  "Return the isolation-tier face for environment ENV, or `default' if unknown."
  (or (nth 1 (assoc env safeslop-portal--env-tiers))
      'default))

(defun safeslop-portal--env-cell (env)
  "Return ENV as a tier-coloured tabulated-list cell with its honest note as help-echo.
The text label is always present, so colour is a redundant reinforcement, not the
sole signal (specs/0031).  An unknown env renders plainly."
  (let* ((row (assoc env safeslop-portal--env-tiers)))
    (if row
        (propertize env
                    'face (nth 1 row)
                    'help-echo (format "%s — %s" (nth 2 row) (nth 3 row)))
      env)))

(defun safeslop-portal--tier-legend ()
  "Return a one-line isolation-tier ramp legend (host most dangerous -> container safest)."
  (concat
   "tiers: "
   (mapconcat (lambda (row)
                (propertize (concat (car row) "=" (nth 2 row)) 'face (nth 1 row)))
              safeslop-portal--env-tiers "  ")
   "\n\n"))

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

(defun safeslop-portal--sessions-from (envelope)
  "Return the parsed sessions (a list of alists) from a `session list' ENVELOPE.
On a failed list (e.g. a stale binary), surface the error in the echo area so the
empty table is not silently mysterious."
  (unless (safeslop-contract-ok-p envelope)
    (message "safeslop portal: %s"
             (or (alist-get 'message (car (safeslop-contract-errors envelope)))
                 "session list failed")))
  (alist-get 'sessions (safeslop-contract-data envelope)))

(defun safeslop-portal--rows (sessions)
  "Build `tabulated-list-entries' from SESSIONS (a list of alists), status-ordered.
Pure: SESSIONS is already-fetched data, so the row builder never blocks on I/O."
  (mapcar
   (lambda (sess)
     (let ((id (safeslop-portal--field sess 'session_id)))
       (list id
             (vector (safeslop-portal--short-id id)
                     (safeslop-portal--field sess 'agent)
                     (safeslop-portal--env-cell (safeslop-portal--field sess 'environment))
                     (safeslop-portal--field sess 'network)
                     (safeslop-portal--status-cell (safeslop-portal--field sess 'status))
                     (safeslop-portal--pid sess)
                     (safeslop-portal--age sess)
                     (safeslop-portal--recipe-cell sess)
                     (safeslop-portal--image-cell sess)
                     (abbreviate-file-name (safeslop-portal--field sess 'workspace))))))
   (sort (copy-sequence sessions)
         (lambda (a b)
           (string< (safeslop-portal--field a 'status)
                    (safeslop-portal--field b 'status))))))

(defun safeslop-portal--id-at-point ()
  "Return the session id on the current line, or signal a user error."
  (or (tabulated-list-get-id)
      (user-error "No session on this line")))

(defun safeslop-portal-open ()
  "Open (run) the agent for the session at point in a term buffer."
  (interactive)
  (safeslop-session-attach (safeslop-portal--id-at-point)))

(defun safeslop-portal-reattach ()
  "Reattach to the detached supervisor of the session at point."
  (interactive)
  (safeslop-session-reattach (safeslop-portal--id-at-point)))

(defun safeslop-portal-status ()
  "Show status for the session at point."
  (interactive)
  (safeslop-session-status (safeslop-portal--id-at-point)))

(defun safeslop-portal-stop ()
  "Stop the session at point (revoking credentials) and refresh."
  (interactive)
  (let ((id (safeslop-portal--id-at-point)))
    (when (yes-or-no-p (format "Stop session %s (revoke credentials)? " id))
      (safeslop-session-stop id)
      (safeslop-portal-refresh))))

(defun safeslop-portal-new ()
  "Create a new session.
`safeslop-session-new' is async, so it reveals the new session in the portal
itself once the create completes (via `safeslop-portal--reveal-session') — a
refresh here would race the still-running create."
  (interactive)
  (call-interactively #'safeslop-session-new))

(defconst safeslop-portal--key-hints
  '(("RET" . "open") ("R" . "reattach") ("i" . "status") ("k" . "stop")
    ("n" . "new") ("g" . "refresh") ("a" . "auto") ("d" . "doctor")
    ("L" . "debug") ("?" . "help") ("q" . "quit"))
  "Key/action pairs shown in the portal's in-buffer shortcut legend.")

(defun safeslop-portal--legend ()
  "Return the shortcut legend line (keys faced as bindings), trailing blank line."
  (concat (mapconcat (lambda (pair)
                       (concat (propertize (car pair) 'face 'help-key-binding)
                               " " (cdr pair)))
                     safeslop-portal--key-hints "  ")
          "\n\n"))

(defun safeslop-portal--header ()
  "Return the portal header block: surface tab strip, tier legend, shortcut legend."
  (concat (safeslop-surface--tab-strip 'sessions)
          (safeslop-portal--tier-legend)
          (safeslop-portal--legend)))

(defun safeslop-portal--goto-first-row ()
  "Move point to the first tabulated session row, past the header block."
  (goto-char (point-min))
  (while (and (not (tabulated-list-get-id)) (not (eobp)))
    (forward-line 1)))

(defun safeslop-portal--goto-id (id)
  "Move point to the row whose session id is ID; return non-nil when found."
  (goto-char (point-min))
  (let (found)
    (while (and (not found) (not (eobp)))
      (if (equal (tabulated-list-get-id) id)
          (setq found t)
        (forward-line 1)))
    found))

(defun safeslop-portal--render (&optional keep-point after)
  "Asynchronously fetch the session list, then fill the current portal buffer:
the surface tab strip, tier legend, shortcut legend, then the session table.
Non-blocking: the `session list' fetch runs in a subprocess and the redraw
happens in its callback, so neither a manual `g' nor the auto-refresh timer ever
freezes Emacs (the whole point of the timer is that it must not block).  The
header is plain buffer text above the rows (the column titles stay in the window
header line).  With KEEP-POINT non-nil, stay on the same session across the
reprint (for auto-refresh and `g'); otherwise land on the first row (for a fresh
open).  AFTER, when given, is called with point in the buffer once the redraw is
done (used to reveal a just-created session)."
  (let ((buf (current-buffer)))
    (safeslop--call-json-async
     '("session" "list" "--output" "json")
     (lambda (envelope)
       (when (buffer-live-p buf)
         (with-current-buffer buf
           (setq tabulated-list-entries
                 (safeslop-portal--rows (safeslop-portal--sessions-from envelope)))
           ;; `tabulated-list-print' erases the buffer (header included) and reprints;
           ;; with KEEP-POINT it restores point to the same entry id, so the header
           ;; re-insert above (before point) shifts but does not move it off the session.
           (tabulated-list-print keep-point)
           (let ((inhibit-read-only t))
             (save-excursion
               (goto-char (point-min))
               (insert (safeslop-portal--header))))
           (unless keep-point
             (safeslop-portal--goto-first-row))
           (when after (funcall after))))))))

(defun safeslop-portal--reveal-session (id)
  "If a live portal exists, refresh it and land point on session ID.
Called after `safeslop-session-new' creates ID so the new session shows up at
once — the create is async, so a plain refresh would race it."
  (let ((buf (get-buffer safeslop-portal-buffer-name)))
    (when buf
      (with-current-buffer buf
        (safeslop-portal--render t (lambda () (safeslop-portal--goto-id id)))))))

(defun safeslop-portal-refresh ()
  "Re-fetch the session list and redraw the portal, keeping point on its session."
  (interactive)
  (let ((buf (get-buffer safeslop-portal-buffer-name)))
    (when buf
      (with-current-buffer buf
        (safeslop-portal--render t)))))

(defun safeslop-portal--cancel-timer ()
  "Cancel the portal auto-refresh timer if one is running."
  (when safeslop-portal--timer
    (cancel-timer safeslop-portal--timer)
    (setq safeslop-portal--timer nil)))

(defun safeslop-portal--auto-refresh ()
  "Timer callback: refresh the portal when it is live, shown, and not prompting.
Self-cancels once the portal buffer is gone; skips a tick while any minibuffer is
active so it never fights a `k'-stop confirmation or other prompt."
  (let ((buf (get-buffer safeslop-portal-buffer-name)))
    (cond
     ((not (buffer-live-p buf)) (safeslop-portal--cancel-timer))
     ((and (get-buffer-window buf 'visible)
           (not (active-minibuffer-window)))
      (safeslop-portal-refresh)))))

(defun safeslop-portal--start-timer ()
  "(Re)start the auto-refresh timer per `safeslop-portal-refresh-interval'.
A nil or non-positive interval leaves the portal static (manual `g' only)."
  (safeslop-portal--cancel-timer)
  (when (and (numberp safeslop-portal-refresh-interval)
             (> safeslop-portal-refresh-interval 0))
    (setq safeslop-portal--timer
          (run-at-time safeslop-portal-refresh-interval
                       safeslop-portal-refresh-interval
                       #'safeslop-portal--auto-refresh))))

(defun safeslop-portal-toggle-auto-refresh ()
  "Toggle the portal's automatic refresh on or off for this Emacs session."
  (interactive)
  (if safeslop-portal--timer
      (progn (safeslop-portal--cancel-timer)
             (message "safeslop portal: auto-refresh off (g to refresh)"))
    (if (and (numberp safeslop-portal-refresh-interval)
             (> safeslop-portal-refresh-interval 0))
        (progn (safeslop-portal--start-timer)
               (message "safeslop portal: auto-refresh every %ss"
                        safeslop-portal-refresh-interval))
      (user-error "Set `safeslop-portal-refresh-interval' to a positive number first"))))

(defvar safeslop-portal-mode-map
  (let ((map (make-sparse-keymap)))
    (define-key map (kbd "RET") #'safeslop-portal-open)
    (define-key map (kbd "o")   #'safeslop-portal-open)
    (define-key map (kbd "R")   #'safeslop-portal-reattach)
    (define-key map (kbd "i")   #'safeslop-portal-status)
    (define-key map (kbd "k")   #'safeslop-portal-stop)
    (define-key map (kbd "n")   #'safeslop-portal-new)
    (define-key map (kbd "g")   #'safeslop-portal-refresh)
    (define-key map (kbd "a")   #'safeslop-portal-toggle-auto-refresh)
    (define-key map (kbd "d")   #'safeslop-doctor)
    (define-key map (kbd "L")   #'safeslop-debug-log)
    (define-key map (kbd "?")   #'describe-mode)
    (define-key map (kbd "q")   #'quit-window)
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
        [("Session" 17 nil)
         ("Agent" 12 nil)
         ("Env" 10 nil)
         ("Net" 5 nil)
         ("Status" 10 nil)
         ("PID" 7 nil)
         ("Age" 6 nil)
         ("Recipe" 24 nil)
         ("Image" 13 nil)
         ("Workspace" 32 nil)])
  (setq tabulated-list-padding 1)
  (tabulated-list-init-header)
  ;; Stop the shared auto-refresh timer when the dashboard goes away.
  (add-hook 'kill-buffer-hook #'safeslop-portal--cancel-timer nil t))

;;;###autoload
(defun safeslop-portal ()
  "Open the safeslop session portal: a dashboard of sessions you can act on.
Keys: RET/o open (run), R reattach, i status, k stop, n new, g refresh,
a toggle auto-refresh, d doctor, L debug log, q quit.

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
    ;; Start (or restart) the shared auto-refresh timer for the live dashboard.
    (safeslop-portal--start-timer)
    buf))

;;;###autoload
(defalias 'safeslop #'safeslop-portal
  "Open the safeslop session portal (alias for `safeslop-portal').")

(provide 'safeslop-portal)
;;; safeslop-portal.el ends here
