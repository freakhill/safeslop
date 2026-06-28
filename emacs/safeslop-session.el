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
(declare-function safeslop--call-json-async "safeslop" (args callback))
(declare-function safeslop--show-envelope-buffer "safeslop" (name args envelope))
(declare-function safeslop-portal--reveal-session "safeslop-portal" (id))

(defun safeslop-session--create-args (agent workspace &optional environment network)
  "Return exact argv for creating a session with AGENT in WORKSPACE.
ENVIRONMENT (host|container|vm) and NETWORK (deny|allow), when a non-empty
string, are appended as `--environment'/`--network' arguments (specs/0074).
ENVIRONMENT is required by the engine (specs/0053 removed the sandbox default);
the interactive picker always supplies one.  These precede `--output json'."
  (append
   (list "session" "create" "--agent" agent "--workspace" (expand-file-name workspace))
   (when (and (stringp environment) (not (string-empty-p environment)))
     (list "--environment" environment))
   (when (and (stringp network) (not (string-empty-p network)))
     (list "--network" network))
   (list "--output" "json")))

;;;###autoload
(defun safeslop-session-new (&optional agent workspace callback environment network)
  "Create a safeslop session for AGENT in WORKSPACE and show the JSON envelope.
ENVIRONMENT (host|container|vm) and NETWORK (deny|allow) set the session's
isolation/network for this run (specs/0074; environment is required, specs/0053);
interactively they are prompted with container/deny preselected.  Runs
asynchronously (session create may stage credentials, which can be slow), so
Emacs does not block.  CALLBACK, when given, is called with the envelope once it
arrives (used by tests); it precedes ENVIRONMENT/NETWORK so existing callers and
the security argv tests keep their three-argument form."
  (interactive
   (list (completing-read "Agent: " '("claude" "claude-code" "pi") nil t nil nil "claude")
         (read-directory-name "Workspace: " nil nil t)
         nil ; callback: interactive use shows the envelope buffer, no extra hook
         (completing-read "Environment: " '("container" "vm" "host") nil t nil nil "container")
         (completing-read "Network: " '("deny" "allow") nil t nil nil "deny")))
  (let ((args (safeslop-session--create-args (or agent "claude")
                                             (or workspace default-directory)
                                             environment network)))
    (safeslop--call-json-async
     args
     (lambda (envelope)
       (safeslop--show-envelope-buffer "*safeslop session*" args envelope)
       (let ((id (and (safeslop-contract-ok-p envelope)
                      (alist-get 'session_id (safeslop-contract-data envelope)))))
         ;; Reveal the new session in a live portal (the create is async, so a
         ;; plain portal refresh would race it), and — only when driven
         ;; interactively (no test CALLBACK) — offer to open it right away so
         ;; "created" leads straight to an obvious access path (specs/0052 #3).
         (when id
           (when (fboundp 'safeslop-portal--reveal-session)
             (safeslop-portal--reveal-session id))
           (when (null callback)
             (safeslop-session--offer-open id))))
       (when callback (funcall callback envelope))))))

(defun safeslop-session--offer-open (id)
  "Offer to open (attach) the freshly created session ID right now."
  (when (y-or-n-p (format "Open session %s now? " id))
    (safeslop-session-attach id)))

(defun safeslop-session--run-args (session-id)
  "Return exact argv for running SESSION-ID."
  (list "session" "run" "--session-id" session-id))

(defun safeslop-session--attach-args (session-id)
  "Return exact argv for reattaching to SESSION-ID's detached supervisor."
  (list "session" "attach" "--session-id" session-id))

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

(defun safeslop-session--launch-term (session-id argv)
  "Launch ARGV for SESSION-ID under a built-in term-mode PTY; return the buffer.
If the process reports the PTY_UNAVAILABLE contract error (no usable controlling
terminal), switch to the read-only JSONL status fallback
\(`safeslop-session-status-fallback')."
  (let ((buf (apply #'make-term (concat "safeslop-" session-id)
                    safeslop-program nil argv)))
    (with-current-buffer buf
      (term-mode)
      (term-char-mode))
    (let ((proc (get-buffer-process buf)))
      (when proc
        ;; Capture raw stdout ahead of term's renderer, then key on it when the
        ;; process exits.  add-function (not set-process-*) preserves term's own
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
        ;; Backstop for a process that already exited before the sentinel was wired.
        (unless (process-live-p proc)
          (safeslop-session--maybe-status-fallback buf session-id))))
    (pop-to-buffer buf)
    buf))

;;;###autoload
(defun safeslop-session-attach (&optional session-id)
  "Attach to SESSION-ID by running its agent under a built-in term-mode PTY.
On PTY_UNAVAILABLE (no usable controlling terminal), switch to the read-only
JSONL status fallback (`safeslop-session-status-fallback')."
  (interactive (list (read-string "Session id: ")))
  (safeslop-session--launch-term session-id (safeslop-session--run-args session-id)))

;;;###autoload
(defun safeslop-session-list (&optional callback)
  "List safeslop sessions via the JSON contract, asynchronously.
CALLBACK, when given, is called with the envelope once it arrives (used by tests)."
  (interactive)
  (let ((args '("session" "list" "--output" "json")))
    (safeslop--call-json-async
     args
     (lambda (envelope)
       (safeslop--show-envelope-buffer "*safeslop sessions*" args envelope)
       (when callback (funcall callback envelope))))))

;;;###autoload
(defun safeslop-session-status (&optional session-id callback)
  "Show SESSION-ID status via the JSON contract, asynchronously.
CALLBACK, when given, is called with the envelope once it arrives (used by tests)."
  (interactive (list (read-string "Session id: ")))
  (let ((args (list "session" "status" "--session-id" session-id "--output" "json")))
    (safeslop--call-json-async
     args
     (lambda (envelope)
       (safeslop--show-envelope-buffer "*safeslop session status*" args envelope)
       (when callback (funcall callback envelope))))))

;;;###autoload
(defun safeslop-session-stop (&optional session-id callback)
  "Stop SESSION-ID, revoking credentials, asynchronously, and show the envelope.
Credential revocation can take a moment, so the call is async and never blocks
Emacs.  CALLBACK, when given, is called with the envelope once it arrives (tests)."
  (interactive (list (read-string "Session id: ")))
  (let ((args (list "session" "stop" "--session-id" session-id "--revoke-credentials" "--output" "json")))
    (safeslop--call-json-async
     args
     (lambda (envelope)
       (safeslop--show-envelope-buffer "*safeslop session stop*" args envelope)
       (when callback (funcall callback envelope))))))

;;;###autoload
(defun safeslop-session-reattach (&optional session-id)
  "Reattach to SESSION-ID's detached supervisor over its socket, under a built-in
term-mode PTY.  Unlike `safeslop-session-attach' (which runs the agent coupled),
this rejoins an agent already running under a detached supervisor (specs/0051).
On PTY_UNAVAILABLE (no usable controlling terminal), switch to the read-only
JSONL status fallback (`safeslop-session-status-fallback')."
  (interactive (list (read-string "Session id: ")))
  (safeslop-session--launch-term session-id (safeslop-session--attach-args session-id)))

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
