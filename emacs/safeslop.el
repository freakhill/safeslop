;;; safeslop.el --- Emacs frontend for safeslop -*- lexical-binding: t; -*-

;; Copyright (C) 2026

;; Author: safeslop
;; Version: 0.1.0
;; Package-Requires: ((emacs "32.0"))
;; Keywords: tools, processes, ai

;;; Commentary:

;; Raw Emacs entry points for safeslop.  The package intentionally avoids Doom
;; APIs in core; Doom integration lives in the optional safeslop-doom.el shim.

;;; Code:

(require 'subr-x)

(defgroup safeslop nil
  "Run safeslop from Emacs."
  :group 'tools)

(defcustom safeslop-program "safeslop"
  "Path to the safeslop CLI."
  :type 'file
  :group 'safeslop)

(defcustom safeslop-autostart-daemon t
  "When non-nil, try to autostart the safeslop daemon when no socket is up."
  :type 'boolean
  :group 'safeslop)

(defcustom safeslop-daemon-program nil
  "Path to the safeslop daemon binary, or nil to auto-resolve.
Resolution precedence when nil: $SAFESLOP_DAEMON_BIN, then `safeslopd', then
`safeslop-mcp' on `exec-path'.  Current safeslop releases may not ship a daemon;
in that case autostart is a no-op until this variable points at one."
  :type '(choice (const :tag "Auto-resolve" nil) (file :tag "Path"))
  :group 'safeslop)

(defcustom safeslop-daemon-state-dir
  (expand-file-name "~/Library/Application Support/safeslop")
  "Directory for the autostarted daemon's socket, log, and state."
  :type 'directory
  :group 'safeslop)

(defcustom safeslop-daemon-socket nil
  "Path to the safeslop daemon socket, or nil for STATE-DIR/safeslop.sock."
  :type '(choice (const :tag "STATE-DIR/safeslop.sock" nil) (file :tag "Path"))
  :group 'safeslop)

(defcustom safeslop-daemon-args '("serve")
  "Extra daemon arguments appended after `--state-dir DIR --socket SOCKET'.
This mirrors slopmaxx's local daemon autostart shape while leaving the concrete
server binary configurable until safeslop grows a checked-in daemon."
  :type '(repeat string)
  :group 'safeslop)

(defcustom safeslop-daemon-startup-timeout 10
  "Seconds to wait for an autostarted daemon socket before continuing."
  :type 'number
  :group 'safeslop)

(defvar safeslop-last-error nil
  "Last error surfaced by a safeslop command.")

(defvar safeslop-output-mode-map
  (let ((map (make-sparse-keymap)))
    (define-key map (kbd "g") #'safeslop-doctor)
    (define-key map (kbd "e") #'safeslop-show-last-error)
    (define-key map (kbd "q") #'quit-window)
    map)
  "Keymap for `safeslop-output-mode'.")

(define-derived-mode safeslop-output-mode special-mode "safeslop"
  "Major mode for read-only safeslop command output buffers."
  (setq-local truncate-lines t))

(defun safeslop--daemon-state-dir ()
  "Return `safeslop-daemon-state-dir' as a directory path."
  (file-name-as-directory (expand-file-name safeslop-daemon-state-dir)))

(defun safeslop-daemon-socket-path ()
  "Return the safeslop daemon socket path."
  (expand-file-name (or safeslop-daemon-socket "safeslop.sock")
                    (safeslop--daemon-state-dir)))

(defun safeslop-daemon-live-p ()
  "Return non-nil when the configured safeslop daemon socket appears to be up."
  (let ((socket (safeslop-daemon-socket-path)))
    (and (file-exists-p socket)
         (or (not (fboundp 'file-socket-p))
             (file-socket-p socket)))))

(defun safeslop--resolve-daemon-program ()
  "Resolve the safeslop daemon binary path, or nil when none is found."
  (or (let ((custom (and safeslop-daemon-program
                         (expand-file-name safeslop-daemon-program))))
        (and custom (file-executable-p custom) custom))
      (let* ((env (getenv "SAFESLOP_DAEMON_BIN"))
             (path (and env (not (string-empty-p env)) (expand-file-name env))))
        (and path (file-executable-p path) path))
      (executable-find "safeslopd")
      (executable-find "safeslop-mcp")))

(defun safeslop--daemon-command (program)
  "Build the argv list to spawn PROGRAM as the safeslop daemon."
  (let ((dir (safeslop--daemon-state-dir)))
    (append (list program
                  "--state-dir" dir
                  "--socket" (safeslop-daemon-socket-path))
            safeslop-daemon-args)))

;;;###autoload
(defun safeslop-daemon-start ()
  "Start the configured safeslop daemon if a daemon binary is available.
Return the daemon log path when a process is spawned, nil otherwise."
  (interactive)
  (if (safeslop-daemon-live-p)
      (progn
        (message "safeslop daemon already appears up at %s" (safeslop-daemon-socket-path))
        nil)
    (let ((program (safeslop--resolve-daemon-program)))
      (if (not program)
          (progn
            (message "No safeslop daemon binary found; set `safeslop-daemon-program' or SAFESLOP_DAEMON_BIN")
            nil)
        (let* ((dir (safeslop--daemon-state-dir))
               (log (expand-file-name "daemon.log" dir))
               (argv (safeslop--daemon-command program))
               (quoted (mapconcat #'shell-quote-argument argv " ")))
          (make-directory dir t)
          ;; Match slopmaxx's developer UX: use nohup so the daemon can outlive
          ;; Emacs.  Each argv element is shell-quoted before interpolation.
          (call-process "sh" nil 0 nil "-c"
                        (format "nohup %s >>%s 2>&1 &"
                                quoted (shell-quote-argument log)))
          (message "started safeslop daemon; log: %s" log)
          log)))))

(defun safeslop-daemon-wait (&optional timeout)
  "Wait until the daemon socket exists or TIMEOUT seconds elapse."
  (let ((deadline (+ (float-time) (or timeout safeslop-daemon-startup-timeout))))
    (catch 'ready
      (while (< (float-time) deadline)
        (when (safeslop-daemon-live-p)
          (throw 'ready t))
        (sleep-for 0.2))
      nil)))

(defun safeslop--ensure-daemon ()
  "Ensure a safeslop daemon is up when autostart is configured.
Return non-nil if the socket is up after this call."
  (or (safeslop-daemon-live-p)
      (when safeslop-autostart-daemon
        (when (safeslop-daemon-start)
          (safeslop-daemon-wait)))))

(defun safeslop--run-buffer (name args)
  "Run safeslop with ARGS into buffer NAME using exact argv, never a shell."
  (safeslop--ensure-daemon)
  (let ((buf (get-buffer-create name)))
    (with-current-buffer buf
      (let ((inhibit-read-only t))
        (erase-buffer)
        (insert (format "$ %s %s\n\n" safeslop-program (string-join args " ")))
        (safeslop-output-mode)))
    (let ((status (apply #'call-process safeslop-program nil buf t args)))
      (unless (equal status 0)
        (setq safeslop-last-error
              (format "safeslop exited with status %s: %s" status (string-join args " "))))
      (pop-to-buffer buf)
      status)))

;;;###autoload
(defun safeslop-doctor ()
  "Run `safeslop doctor'."
  (interactive)
  (safeslop--run-buffer "*safeslop doctor*" '("doctor")))

;;;###autoload
(defun safeslop-policy-check-file (file)
  "Validate safeslop policy FILE."
  (interactive (list (read-file-name "Policy file: " nil nil t "safeslop.cue")))
  (safeslop--run-buffer "*safeslop validate*" (list "validate" (expand-file-name file))))

;;;###autoload
(defun safeslop-session-new (&optional agent workspace)
  "Create a placeholder safeslop session for AGENT in WORKSPACE.
Session APIs land in specs/0049 PR5; this command currently records the
requested intent and avoids invoking non-existent CLI surfaces."
  (interactive
   (list (completing-read "Agent: " '("claude" "pi") nil t nil nil "claude")
         (read-directory-name "Workspace: " nil nil t)))
  (message "safeslop sessions are not implemented yet (agent=%s workspace=%s)" agent workspace))

;;;###autoload
(defun safeslop-session-attach ()
  "Attach to a safeslop session placeholder."
  (interactive)
  (message "safeslop session attach is not implemented yet"))

;;;###autoload
(defun safeslop-session-list ()
  "List safeslop sessions placeholder."
  (interactive)
  (message "safeslop session list is not implemented yet"))

;;;###autoload
(defun safeslop-session-status ()
  "Show safeslop session status placeholder."
  (interactive)
  (message "safeslop session status is not implemented yet"))

;;;###autoload
(defun safeslop-session-stop ()
  "Stop a safeslop session placeholder."
  (interactive)
  (message "safeslop session stop is not implemented yet"))

;;;###autoload
(defun safeslop-session-restart ()
  "Restart a safeslop session placeholder."
  (interactive)
  (message "safeslop session restart is not implemented yet"))

;;;###autoload
(defun safeslop-switch-to-session-buffer ()
  "Switch to the latest safeslop buffer."
  (interactive)
  (let ((buf (or (get-buffer "*safeslop doctor*")
                 (get-buffer "*safeslop validate*"))))
    (if buf
        (pop-to-buffer buf)
      (message "No safeslop buffer yet"))))

;;;###autoload
(defun safeslop-show-last-error ()
  "Show the last safeslop command error."
  (interactive)
  (if safeslop-last-error
      (message "%s" safeslop-last-error)
    (message "No safeslop error recorded")))

;;;###autoload
(defun safeslop-help ()
  "Show safeslop Emacs help."
  (interactive)
  (message "safeslop: C-c s d doctor, C-c s p validate policy, C-c s n new session"))

(defvar safeslop-command-map
  (let ((map (make-sparse-keymap)))
    (define-key map (kbd "D") #'safeslop-daemon-start)
    (define-key map (kbd "d") #'safeslop-doctor)
    (define-key map (kbd "p") #'safeslop-policy-check-file)
    (define-key map (kbd "n") #'safeslop-session-new)
    (define-key map (kbd "a") #'safeslop-session-attach)
    (define-key map (kbd "l") #'safeslop-session-list)
    (define-key map (kbd "t") #'safeslop-session-status)
    (define-key map (kbd "s") #'safeslop-session-stop)
    (define-key map (kbd "r") #'safeslop-session-restart)
    (define-key map (kbd "b") #'safeslop-switch-to-session-buffer)
    (define-key map (kbd "e") #'safeslop-show-last-error)
    (define-key map (kbd "?") #'safeslop-help)
    map)
  "Prefix command map for safeslop.")
(fset 'safeslop-command-map safeslop-command-map)

;;;###autoload
(defun safeslop-bind-default-keys ()
  "Bind safeslop commands under `C-c s'."
  (interactive)
  (define-key global-map (kbd "C-c s") #'safeslop-command-map))

(provide 'safeslop)
;;; safeslop.el ends here
