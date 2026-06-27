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
(require 'safeslop-contract)

(defvar safeslop-program)
(declare-function safeslop--call-json "safeslop" (args))
(declare-function safeslop-doctor "safeslop" ())
(declare-function safeslop-debug-log "safeslop" ())
(declare-function safeslop-session-new "safeslop-session" (&optional agent workspace))
(declare-function safeslop-session-attach "safeslop-session" (&optional session-id))
(declare-function safeslop-session-reattach "safeslop-session" (&optional session-id))
(declare-function safeslop-session-status "safeslop-session" (&optional session-id))
(declare-function safeslop-session-stop "safeslop-session" (&optional session-id))

(defconst safeslop-portal-buffer-name "*safeslop portal*"
  "Buffer name for the safeslop session dashboard.")

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

(defun safeslop-portal--pid (sess)
  "Return SESS's pid as a display string, or an em dash when it has none."
  (let ((pid (safeslop-portal--field sess 'pid)))
    (if (string-empty-p pid) "—" pid)))

(defun safeslop-portal--sessions ()
  "Fetch the session list and return the parsed sessions (a list of alists).
On a failed `session list' (e.g. a stale binary), surface the error in the echo
area so the empty table is not silently mysterious."
  (let ((envelope (safeslop--call-json '("session" "list" "--output" "json"))))
    (unless (safeslop-contract-ok-p envelope)
      (message "safeslop portal: %s"
               (or (alist-get 'message (car (safeslop-contract-errors envelope)))
                   "session list failed")))
    (alist-get 'sessions (safeslop-contract-data envelope))))

(defun safeslop-portal--rows ()
  "Build `tabulated-list-entries' from the live session list, status-ordered."
  (mapcar
   (lambda (sess)
     (let ((id (safeslop-portal--field sess 'session_id)))
       (list id
             (vector (safeslop-portal--short-id id)
                     (safeslop-portal--field sess 'agent)
                     (safeslop-portal--field sess 'environment)
                     (safeslop-portal--field sess 'network)
                     (safeslop-portal--status-cell (safeslop-portal--field sess 'status))
                     (safeslop-portal--pid sess)
                     (abbreviate-file-name (safeslop-portal--field sess 'workspace))))))
   (sort (copy-sequence (safeslop-portal--sessions))
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
  "Create a new session, then refresh the portal."
  (interactive)
  (call-interactively #'safeslop-session-new)
  (safeslop-portal-refresh))

(defconst safeslop-portal--key-hints
  '(("RET" . "open") ("R" . "reattach") ("i" . "status") ("k" . "stop")
    ("n" . "new") ("g" . "refresh") ("d" . "doctor") ("L" . "debug")
    ("?" . "help") ("q" . "quit"))
  "Key/action pairs shown in the portal's in-buffer shortcut legend.")

(defun safeslop-portal--legend ()
  "Return the shortcut legend line (keys faced as bindings), trailing blank line."
  (concat (mapconcat (lambda (pair)
                       (concat (propertize (car pair) 'face 'help-key-binding)
                               " " (cdr pair)))
                     safeslop-portal--key-hints "  ")
          "\n\n"))

(defun safeslop-portal--render ()
  "Fill the current portal buffer: the shortcut legend, then the session table.
Like slopmaxx's console, the legend is plain buffer text above the rows (the
column titles stay in the window header line)."
  (setq tabulated-list-entries (safeslop-portal--rows))
  (tabulated-list-print)
  (let ((inhibit-read-only t))
    (save-excursion
      (goto-char (point-min))
      (insert (safeslop-portal--legend))))
  ;; Land on the first session row, past the legend + its blank line.
  (goto-char (point-min))
  (forward-line 2))

(defun safeslop-portal-refresh ()
  "Re-fetch the session list and redraw the portal."
  (interactive)
  (let ((buf (get-buffer safeslop-portal-buffer-name)))
    (when buf
      (with-current-buffer buf
        (safeslop-portal--render)))))

(defvar safeslop-portal-mode-map
  (let ((map (make-sparse-keymap)))
    (define-key map (kbd "RET") #'safeslop-portal-open)
    (define-key map (kbd "o")   #'safeslop-portal-open)
    (define-key map (kbd "R")   #'safeslop-portal-reattach)
    (define-key map (kbd "i")   #'safeslop-portal-status)
    (define-key map (kbd "k")   #'safeslop-portal-stop)
    (define-key map (kbd "n")   #'safeslop-portal-new)
    (define-key map (kbd "g")   #'safeslop-portal-refresh)
    (define-key map (kbd "d")   #'safeslop-doctor)
    (define-key map (kbd "L")   #'safeslop-debug-log)
    (define-key map (kbd "?")   #'describe-mode)
    (define-key map (kbd "q")   #'quit-window)
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
         ("Workspace" 38 nil)])
  (setq tabulated-list-padding 1)
  (tabulated-list-init-header))

;;;###autoload
(defun safeslop-portal ()
  "Open the safeslop session portal: a dashboard of sessions you can act on.
Keys: RET/o open (run), R reattach, i status, k stop, n new, g refresh,
d doctor, L debug log, q quit."
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
    buf))

;;;###autoload
(defalias 'safeslop #'safeslop-portal
  "Open the safeslop session portal (alias for `safeslop-portal').")

(provide 'safeslop-portal)
;;; safeslop-portal.el ends here
