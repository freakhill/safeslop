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

(defun safeslop--error-envelope (code message)
  "Build a client-side error envelope alist carrying CODE and MESSAGE.
Shaped so the `safeslop-contract-*' accessors and the output renderer treat it
like a real failed envelope."
  (list (cons 'schema_version 1)
        (cons 'ok :json-false)
        (cons 'data nil)
        (cons 'warnings nil)
        (cons 'errors (list (list (cons 'code code) (cons 'message message))))))

(defun safeslop--call-json (args)
  "Run safeslop with ARGS and parse stdout as a contract envelope.
ARGS is passed to `call-process' as an argv list; no shell is used.  safeslop is
a self-contained CLI (no daemon round-trip), so each command is a direct
subprocess; the call and its result are recorded in the debug log.

Degrades gracefully: a missing program or non-JSON output (e.g. a stale binary
that predates a subcommand) yields a CLIENT_* error envelope with a clear,
actionable message instead of a raw `json-parse-error' crash."
  (safeslop--debug :event 'call :argv (string-join args " "))
  (with-temp-buffer
    (let ((status (condition-case err
                      (apply #'call-process safeslop-program nil t nil args)
                    (error (insert (error-message-string err)) -1))))
      (let* ((stdout (buffer-string))
             (envelope (condition-case _err
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
            (safeslop--error-envelope "CLIENT_NON_JSON" msg)))))))

(defun safeslop--scalar (v)
  "Render a parsed JSON scalar V as a display string.
Matches the contract parser's :json-false/:json-null sentinels."
  (cond ((eq v t) "true")
        ((memq v '(:false :json-false)) "false")
        ((memq v '(:null :json-null)) "null")
        ((stringp v) v)
        ((numberp v) (number-to-string v))
        (t (format "%S" v))))

(defun safeslop--alist-p (x)
  "Return non-nil when X is a parsed JSON object (a symbol-keyed alist)."
  (and (consp x) (consp (car x)) (symbolp (caar x))))

(defun safeslop--insert-data (data indent)
  "Insert parsed envelope DATA readably at point, indented by INDENT levels.
Handles JSON objects (alists), arrays (lists), and scalars."
  (let ((pad (make-string (* 2 indent) ?\s)))
    (cond
     ((safeslop--alist-p data)
      (dolist (kv data)
        (let ((k (car kv)) (v (cdr kv)))
          (cond
           ((safeslop--alist-p v)
            (insert (format "%s%s:\n" pad k))
            (safeslop--insert-data v (1+ indent)))
           ((and (consp v) (safeslop--alist-p (car v)))
            (insert (format "%s%s:\n" pad k))
            (safeslop--insert-data v (1+ indent)))
           ((and (consp v) (not (safeslop--alist-p v)))
            (insert (format "%s%s: %s\n" pad k
                            (mapconcat #'safeslop--scalar v ", "))))
           (t (insert (format "%s%s: %s\n" pad k (safeslop--scalar v))))))))
     ((consp data)
      (dolist (item data)
        (if (safeslop--alist-p item)
            (progn (insert (format "%s-\n" pad))
                   (safeslop--insert-data item (1+ indent)))
          (insert (format "%s- %s\n" pad (safeslop--scalar item))))))
     (t (insert (format "%s%s\n" pad (safeslop--scalar data)))))))

(defun safeslop--show-envelope-buffer (name args envelope)
  "Render ENVELOPE for safeslop ARGS into buffer NAME and return ENVELOPE."
  (let ((buf (get-buffer-create name)))
    (with-current-buffer buf
      (let ((inhibit-read-only t))
        (erase-buffer)
        (insert (format "$ %s %s\n\n" safeslop-program (string-join args " ")))
        (insert (format "ok: %s\n" (if (safeslop-contract-ok-p envelope) "true" "false")))
        (dolist (warning (safeslop-contract-warnings envelope))
          (insert (format "warning[%s]: %s\n"
                          (alist-get 'code warning)
                          (alist-get 'message warning))))
        (dolist (err (safeslop-contract-errors envelope))
          (insert (format "error[%s]: %s\n"
                          (alist-get 'code err)
                          (alist-get 'message err))))
        (let ((data (safeslop-contract-data envelope)))
          (when data
            (insert "\n")
            (safeslop--insert-data data 0)))
        (safeslop-output-mode)))
    (pop-to-buffer buf))
  envelope)

;;;###autoload
(defun safeslop-doctor ()
  "Run `safeslop doctor --json' and parse the contract envelope."
  (interactive)
  (let* ((args '("doctor" "--json"))
         (envelope (safeslop--call-json args)))
    (safeslop--show-envelope-buffer "*safeslop doctor*" args envelope)))

;;;###autoload
(defun safeslop-policy-check-file (file)
  "Validate safeslop policy FILE and parse the contract envelope."
  (interactive (list (read-file-name "Policy file: " nil nil t "safeslop.cue")))
  (let* ((args (list "validate" (expand-file-name file) "--json"))
         (envelope (safeslop--call-json args)))
    (safeslop--show-envelope-buffer "*safeslop validate*" args envelope)))

(require 'safeslop-session)
(require 'safeslop-portal)

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
  (message "safeslop: C-c s P portal, d doctor, n new session, l list, L debug log"))

(defvar safeslop-command-map
  (let ((map (make-sparse-keymap)))
    (define-key map (kbd "P") #'safeslop-portal)
    (define-key map (kbd "d") #'safeslop-doctor)
    (define-key map (kbd "p") #'safeslop-policy-check-file)
    (define-key map (kbd "n") #'safeslop-session-new)
    (define-key map (kbd "a") #'safeslop-session-attach)
    (define-key map (kbd "l") #'safeslop-session-list)
    (define-key map (kbd "t") #'safeslop-session-status)
    (define-key map (kbd "s") #'safeslop-session-stop)
    (define-key map (kbd "r") #'safeslop-session-reattach)
    (define-key map (kbd "b") #'safeslop-switch-to-session-buffer)
    (define-key map (kbd "L") #'safeslop-debug-log)
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
