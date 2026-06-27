;;; safeslop-session.el --- Session commands for safeslop -*- lexical-binding: t; -*-

;; Copyright (C) 2026

;; Author: safeslop
;; Package-Requires: ((emacs "32.0"))
;; Keywords: tools, processes, ai

;;; Commentary:

;; Session-facing Emacs commands.  Attach runs the agent under a built-in
;; term-mode PTY; when the CLI reports the PTY_UNAVAILABLE contract error (no
;; usable controlling terminal), attach switches to the read-only JSONL status
;; fallback so the buffer that started the session stays useful (specs/0050 PR4).

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
   (list (completing-read "Agent: " '("claude" "claude-code" "pi") nil t nil nil "claude")
         (read-directory-name "Workspace: " nil nil t)))
  (let* ((args (safeslop-session--create-args (or agent "claude") (or workspace default-directory)))
         (envelope (safeslop--call-json args)))
    (safeslop--show-envelope-buffer "*safeslop session*" args envelope)))

(defun safeslop-session--run-args (session-id)
  "Return exact argv for running SESSION-ID."
  (list "session" "run" "--session-id" session-id))

(defvar-local safeslop-session--run-output nil
  "Raw stdout accumulated from the `session run' process for this buffer.
Captured before term-mode renders it, so PTY_UNAVAILABLE detection is immune to
terminal line wrapping and term's trailing status line.")

(defvar-local safeslop-session--fallback-done nil
  "Non-nil once this run buffer has switched to the JSONL status fallback.
Guards against the run process's filter and sentinel both triggering the switch.")

(defun safeslop-session--pty-unavailable-p (output)
  "Return non-nil if OUTPUT carries the PTY_UNAVAILABLE contract error code.
A token match on the stable error code, not a strict JSON parse: the run process
is interactive, so its stdout may carry agent banner text around the envelope and
a PTY translates newlines, either of which can defeat a whole-buffer parse."
  (and (stringp output)
       (string-match-p "\"PTY_UNAVAILABLE\"" output)))

(defun safeslop-session--maybe-status-fallback (buf session-id)
  "Switch BUF to the JSONL status fallback if its run reported PTY_UNAVAILABLE.
Idempotent per buffer via `safeslop-session--fallback-done'."
  (when (buffer-live-p buf)
    (with-current-buffer buf
      (when (and (not safeslop-session--fallback-done)
                 (safeslop-session--pty-unavailable-p safeslop-session--run-output))
        (setq safeslop-session--fallback-done t)
        (safeslop-session-status-fallback session-id)))))

;;;###autoload
(defun safeslop-session-attach (&optional session-id)
  "Attach to SESSION-ID using a built-in term-mode PTY and exact argv.
If the run reports the PTY_UNAVAILABLE contract error (no usable controlling
terminal), switch to the read-only JSONL status fallback
\(`safeslop-session-status-fallback')."
  (interactive (list (read-string "Session id: ")))
  (let* ((argv (safeslop-session--run-args session-id))
         (buf (apply #'make-term (concat "safeslop-" session-id)
                     safeslop-program nil argv)))
    (with-current-buffer buf
      (term-mode)
      (term-char-mode))
    (let ((proc (get-buffer-process buf)))
      (when proc
        ;; Capture raw stdout ahead of term's renderer, then key on it when the
        ;; run exits.  add-function (not set-process-*) preserves term's own
        ;; filter/sentinel so the PTY keeps working on the happy path.
        (add-function :before (process-filter proc)
                      (lambda (_p string)
                        (when (buffer-live-p buf)
                          (with-current-buffer buf
                            (setq safeslop-session--run-output
                                  (concat (or safeslop-session--run-output "") string))))))
        (add-function :after (process-sentinel proc)
                      (lambda (p _event)
                        (unless (process-live-p p)
                          (safeslop-session--maybe-status-fallback buf session-id))))
        ;; Backstop for a run that already exited before the sentinel was wired.
        (unless (process-live-p proc)
          (safeslop-session--maybe-status-fallback buf session-id))))
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
