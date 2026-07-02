;;; safeslop-client.el --- CLI subprocess substrate for safeslop -*- lexical-binding: t; -*-

;; Copyright (C) 2026

;; Author: safeslop
;; Package-Requires: ((emacs "32.0"))
;; Keywords: tools, processes, ai

;;; Commentary:

;; The one place the Emacs package talks to the safeslop CLI (specs/0062).
;; Owns the `safeslop' customize group, the program path, the redacted debug
;; log, and the synchronous/asynchronous JSON-contract runners every surface
;; builds on.  No UI beyond the debug/error diagnostics commands lives here;
;; envelope rendering is `safeslop-output', dashboards are the surface files.

;;; Code:

(require 'subr-x)
(require 'cl-lib)
(require 'safeslop-contract)

(defgroup safeslop nil
  "Run safeslop from Emacs."
  :group 'tools)

(defcustom safeslop-program "safeslop"
  "Path to the safeslop CLI."
  :type 'file
  :group 'safeslop)

(defvar safeslop-last-error nil
  "Last error surfaced by a safeslop command.")

;;; Debug log -------------------------------------------------------------
;; A redacted client diagnostics buffer, mirroring slopmaxx's debug log: every
;; CLI invocation and its result land here as one timestamped line so the UI is
;; inspectable.  safeslop never passes secret values as CLI arguments (secrets are
;; resolved by the engine from 1Password / staged dirs), so the argv is safe to log.

(defcustom safeslop-debug-log-enabled t
  "When non-nil, record redacted safeslop client diagnostics to a buffer."
  :type 'boolean
  :group 'safeslop)

(defconst safeslop-debug-buffer-name "*safeslop debug*"
  "Buffer name for safeslop client diagnostics.")

(defun safeslop--debug-format (event)
  "Format a redacted debug EVENT plist as a single log line.
Only allowlisted, non-secret fields are emitted."
  (let (out)
    (cl-loop for (k v) on event by #'cddr do
             (pcase k
               ((or :event :argv :status :ok :error :buffer :detail)
                (push (format "%s=%s" (substring (symbol-name k) 1) v) out))
               (_ nil)))
    (string-join (nreverse out) "  ")))

(defun safeslop--debug (&rest event)
  "Append a redacted debug EVENT plist line to the safeslop debug buffer."
  (when safeslop-debug-log-enabled
    (let ((line (format "%s  %s\n"
                        (format-time-string "%Y-%m-%dT%H:%M:%S.%3N")
                        (safeslop--debug-format event))))
      (with-current-buffer (get-buffer-create safeslop-debug-buffer-name)
        (unless (derived-mode-p 'special-mode)
          (special-mode))
        (let ((inhibit-read-only t))
          (goto-char (point-max))
          (insert line))))))

;;;###autoload
(defun safeslop-debug-log ()
  "Open the safeslop client debug log buffer."
  (interactive)
  (pop-to-buffer (get-buffer-create safeslop-debug-buffer-name))
  (special-mode))

;;;###autoload
(defun safeslop-show-last-error ()
  "Show the last safeslop command error."
  (interactive)
  (if safeslop-last-error
      (message "%s" safeslop-last-error)
    (message "No safeslop error recorded")))

;;; JSON-contract runners ---------------------------------------------------

(defun safeslop--safe-rerun-p (args)
  "Return non-nil when ARGS names a read-only command safe to re-run with `g'."
  (pcase args
    (`("doctor" . ,_) t)
    (`("validate" . ,_) t)
    (`("session" ,(or "list" "status") . ,_) t)
    (`("profile" ,(or "show" "list") . ,_) t)
    (`("catalog" . ,_) t)
    (`("bundle" "list" . ,_) t)
    (`("install" ,(or "status" "plan") . ,_) t)
    (`("install" "apply" "--dry-run" . ,_) t)
    (_ nil)))

(defun safeslop--error-envelope (code message)
  "Build a client-side error envelope alist carrying CODE and MESSAGE.
Shaped so the `safeslop-contract-*' accessors and the output renderer treat it
like a real failed envelope."
  (list (cons 'schema_version 1)
        (cons 'ok :json-false)
        (cons 'data nil)
        (cons 'warnings nil)
        (cons 'errors (list (list (cons 'code code) (cons 'message message))))))

(defvar safeslop--last-call-status nil
  "Exit status of the most recently finished safeslop CLI call.
Set by `safeslop--finish-envelope' (and the async spawn-failure path, as -1)
just before the callback runs.  Callbacks that report process outcome (the
session progress buffer) must read it immediately — sentinels run one at a
time, so the value is stable for the duration of the callback.")

(defun safeslop--finish-envelope (stdout status)
  "Parse CLI STDOUT (process exit STATUS) into a contract envelope, gracefully.
Shared by the synchronous `safeslop--call-json' and the asynchronous
`safeslop--call-json-async': records the result in the debug log, updates
`safeslop-last-error' and `safeslop--last-call-status', and never raises —
non-JSON or empty output (e.g. a stale binary that predates a subcommand) yields
a CLIENT_* error envelope with a clear, actionable message instead of a
`json-parse-error' crash."
  (setq safeslop--last-call-status status)
  (let ((envelope (condition-case _err
                      (safeslop-contract-parse-string stdout)
                    (error nil))))
    (if envelope
        (let ((code (safeslop-contract-first-error-code envelope)))
          (unless (equal status 0)
            (setq safeslop-last-error
                  (or code (format "safeslop exited with status %s" status))))
          (safeslop--debug :event 'result :status status
                           :ok (if (safeslop-contract-ok-p envelope) "t" "nil")
                           :error (or code "-"))
          envelope)
      ;; Non-JSON / unparseable output: surface a useful message, don't crash.
      (let* ((line (string-trim (or (car (split-string stdout "\n" t "[ \t\r]*")) "")))
             (msg (if (string-empty-p line)
                      (format "safeslop produced no output (status %s); is `%s' installed and current? Run `make install'."
                              status safeslop-program)
                    (format "safeslop did not return JSON (status %s): %s — is `%s' current? Run `make install'."
                            status line safeslop-program))))
        (setq safeslop-last-error msg)
        (safeslop--debug :event 'result :status status :ok "nil" :error "non-json")
        (safeslop--error-envelope "CLIENT_NON_JSON" msg)))))

(defvar safeslop--debug-call-event 'call
  "Debug event label for the next safeslop CLI call (`call' or UI `poll').")

(defun safeslop--call-json (args)
  "Run safeslop with ARGS synchronously and parse stdout as a contract envelope.
ARGS is passed to `call-process' as an argv list; no shell is used.  This BLOCKS
Emacs until the subprocess exits — prefer `safeslop--call-json-async' for anything
user-facing so a slow command (credential staging, `doctor' probing the toolchain,
a boundary launch) never freezes the editor.  Kept for the parse-path tests and
any genuinely fast, must-be-synchronous caller.  Degrades gracefully via
`safeslop--finish-envelope'."
  (safeslop--debug :event safeslop--debug-call-event :argv (string-join args " "))
  (with-temp-buffer
    (let ((status (condition-case err
                      (apply #'call-process safeslop-program nil t nil args)
                    (error (insert (error-message-string err)) -1))))
      (safeslop--finish-envelope (buffer-string) status))))

(defun safeslop--call-json-async (args callback &optional stderr)
  "Run safeslop with ARGS asynchronously; call CALLBACK with the parsed envelope.
ARGS is the argv list (no shell).  CALLBACK receives one argument — the contract
envelope (a real one, or a CLIENT_* error envelope on a missing program / non-JSON
output) — and runs in the process sentinel once the subprocess exits, so a slow
command never blocks Emacs's main thread.  STDERR, when non-nil, is a buffer that
receives the process's stderr (used for user-visible progress, e.g. lazy image
builds); stdout stays reserved for the JSON envelope either way.  Returns the
process, or nil when it could not be spawned (CALLBACK is still invoked, with a
client error envelope)."
  (safeslop--debug :event safeslop--debug-call-event :argv (string-join args " "))
  (let ((buf (generate-new-buffer " *safeslop-call*")))
    (condition-case err
        (make-process
         :name "safeslop-call-json"
         :buffer buf
         :command (cons safeslop-program args)
         :connection-type 'pipe
         :noquery t
         :stderr stderr
         :sentinel
         (lambda (proc _event)
           (unless (process-live-p proc)
             (let ((stdout (if (buffer-live-p buf)
                               (with-current-buffer buf (buffer-string))
                             ""))
                   (status (process-exit-status proc)))
               (when (buffer-live-p buf) (kill-buffer buf))
               (funcall callback (safeslop--finish-envelope stdout status))))))
      (error
       (when (buffer-live-p buf) (kill-buffer buf))
       (let ((msg (format "could not run `%s': %s — is it installed? Run `make install'."
                          safeslop-program (error-message-string err))))
         (setq safeslop-last-error msg
               safeslop--last-call-status -1)
         (safeslop--debug :event 'result :status -1 :ok "nil" :error "client-spawn")
         (funcall callback (safeslop--error-envelope "CLIENT_SPAWN" msg))
         nil)))))

(provide 'safeslop-client)
;;; safeslop-client.el ends here
