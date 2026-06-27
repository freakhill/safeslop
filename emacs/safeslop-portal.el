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

(defun safeslop-portal--sessions ()
  "Fetch the session list and return the parsed sessions (a list of alists)."
  (let* ((envelope (safeslop--call-json '("session" "list" "--output" "json")))
         (data (safeslop-contract-data envelope)))
    (alist-get 'sessions data)))

(defun safeslop-portal--rows ()
  "Build `tabulated-list-entries' from the live session list."
  (mapcar
   (lambda (sess)
     (let ((id (safeslop-portal--field sess 'session_id)))
       (list id
             (vector (safeslop-portal--short-id id)
                     (safeslop-portal--field sess 'agent)
                     (safeslop-portal--field sess 'environment)
                     (safeslop-portal--field sess 'network)
                     (safeslop-portal--field sess 'status)
                     (abbreviate-file-name (safeslop-portal--field sess 'workspace))))))
   (safeslop-portal--sessions)))

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

(defun safeslop-portal-refresh ()
  "Re-fetch the session list and redraw the portal, keeping point."
  (interactive)
  (let ((buf (get-buffer safeslop-portal-buffer-name)))
    (when buf
      (with-current-buffer buf
        (setq tabulated-list-entries (safeslop-portal--rows))
        (tabulated-list-print t)
        (when (and tabulated-list-entries (= (point) (point-min)))
          (ignore-errors (forward-line 1)))))))

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
    (define-key map (kbd "q")   #'quit-window)
    map)
  "Keymap for `safeslop-portal-mode'.")

(define-derived-mode safeslop-portal-mode tabulated-list-mode "safeslop-portal"
  "Major mode for the safeslop session dashboard.
\\{safeslop-portal-mode-map}"
  (setq tabulated-list-format
        [("Session" 17 t)
         ("Agent" 12 t)
         ("Env" 10 t)
         ("Net" 5 t)
         ("Status" 10 t)
         ("Workspace" 44 t)])
  (setq tabulated-list-padding 1)
  (setq tabulated-list-sort-key '("Status" . nil))
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
      (setq tabulated-list-entries (safeslop-portal--rows))
      (tabulated-list-print t))
    (pop-to-buffer buf)
    buf))

;;;###autoload
(defalias 'safeslop #'safeslop-portal
  "Open the safeslop session portal (alias for `safeslop-portal').")

(provide 'safeslop-portal)
;;; safeslop-portal.el ends here
