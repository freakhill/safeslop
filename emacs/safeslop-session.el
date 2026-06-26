;;; safeslop-session.el --- Session commands for safeslop -*- lexical-binding: t; -*-

;; Copyright (C) 2026

;; Author: safeslop
;; Package-Requires: ((emacs "32.0"))
;; Keywords: tools, processes, ai

;;; Commentary:

;; Session-facing Emacs commands.  PR4 covers exact argv construction and JSON
;; contract parsing; PR5 will add the actual PTY/status process model.

;;; Code:

(require 'subr-x)
(require 'term)
(require 'safeslop-contract)

(defvar safeslop-program)
(defvar safeslop-last-error)
(declare-function safeslop--call-json "safeslop" (args))
(declare-function safeslop--show-envelope-buffer "safeslop" (name args envelope))

(defun safeslop-session--create-args (agent workspace)
  "Return exact argv for creating a session with AGENT in WORKSPACE."
  (list "session" "create"
        "--agent" agent
        "--workspace" (expand-file-name workspace)
        "--output" "json"))

;;;###autoload
(defun safeslop-session-new (&optional agent workspace)
  "Create a safeslop session for AGENT in WORKSPACE and parse the JSON envelope."
  (interactive
   (list (completing-read "Agent: " '("claude" "pi") nil t nil nil "claude")
         (read-directory-name "Workspace: " nil nil t)))
  (let* ((args (safeslop-session--create-args (or agent "claude") (or workspace default-directory)))
         (envelope (safeslop--call-json args)))
    (safeslop--show-envelope-buffer "*safeslop session*" args envelope)))

(defun safeslop-session--run-args (session-id)
  "Return exact argv for running SESSION-ID."
  (list "session" "run" "--session-id" session-id))

;;;###autoload
(defun safeslop-session-attach (&optional session-id)
  "Attach to SESSION-ID using a built-in term-mode PTY and exact argv."
  (interactive (list (read-string "Session id: ")))
  (let* ((argv (safeslop-session--run-args session-id))
         (buf (apply #'make-term (concat "safeslop-" session-id)
                     safeslop-program nil argv)))
    (with-current-buffer buf
      (term-mode)
      (term-char-mode))
    (pop-to-buffer buf)
    buf))

;;;###autoload
(defun safeslop-session-list ()
  "List safeslop sessions via the JSON contract."
  (interactive)
  (let* ((args '("session" "list" "--output" "json"))
         (envelope (safeslop--call-json args)))
    (safeslop--show-envelope-buffer "*safeslop sessions*" args envelope)))

;;;###autoload
(defun safeslop-session-status (&optional session-id)
  "Show SESSION-ID status via the JSON contract."
  (interactive (list (read-string "Session id: ")))
  (let* ((args (list "session" "status" "--session-id" session-id "--output" "json"))
         (envelope (safeslop--call-json args)))
    (safeslop--show-envelope-buffer "*safeslop session status*" args envelope)))

;;;###autoload
(defun safeslop-session-stop (&optional session-id)
  "Stop SESSION-ID, revoking credentials, and parse the JSON envelope."
  (interactive (list (read-string "Session id: ")))
  (let* ((args (list "session" "stop" "--session-id" session-id "--revoke-credentials" "--output" "json"))
         (envelope (safeslop--call-json args)))
    (safeslop--show-envelope-buffer "*safeslop session stop*" args envelope)))

;;;###autoload
(defun safeslop-session-restart ()
  "Restart a safeslop session placeholder."
  (interactive)
  (message "safeslop session restart lands with the PR5 PTY model"))

;;;###autoload
(defun safeslop-session-status-fallback (&optional session-id)
  "Open a read-only compilation buffer for SESSION-ID JSONL status fallback.
The monitor process is started with an exact argv list; no shell is used."
  (interactive (list (read-string "Session id: ")))
  (let* ((buf (get-buffer-create "*safeslop session status jsonl*"))
         (argv (list safeslop-program "session" "status"
                     "--session-id" session-id "--output" "jsonl")))
    (with-current-buffer buf
      (let ((inhibit-read-only t))
        (erase-buffer)
        (compilation-mode)))
    (make-process :name "safeslop-status-jsonl"
                  :buffer buf
                  :command argv
                  :connection-type 'pipe
                  :noquery t)
    (pop-to-buffer buf)
    buf))

(provide 'safeslop-session)
;;; safeslop-session.el ends here
