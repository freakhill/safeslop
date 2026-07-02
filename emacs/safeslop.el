;;; safeslop.el --- Emacs frontend for safeslop -*- lexical-binding: t; -*-

;; Copyright (C) 2026

;; Author: safeslop
;; Version: 0.1.0
;; Package-Requires: ((emacs "32.0"))
;; Keywords: tools, processes, ai

;;; Commentary:

;; Entry point for the safeslop Emacs package: `(require 'safeslop)' loads the
;; whole operator UI.  The package intentionally avoids Doom APIs in core; Doom
;; integration lives in the optional safeslop-doom.el shim.
;;
;; Module map (specs/0062; each layer only requires the ones above it):
;;
;;   safeslop-contract.el  versioned JSON envelope parse/validate
;;   safeslop-client.el    CLI subprocess substrate + redacted debug log
;;   safeslop-surface.el   shared dashboard chrome + the render engine
;;   safeslop-output.el    read-only envelope output buffers
;;   safeslop-session.el   session commands, terminal attach, detail view
;;   safeslop-portal.el    Sessions dashboard
;;   safeslop-install.el   Install dashboard
;;   safeslop-profiles.el  Profiles dashboard + CUE-backed CRUD
;;   safeslop.el           this file: top-level commands + `C-c s' command map

;;; Code:

(require 'safeslop-contract)
(require 'safeslop-client)
(require 'safeslop-surface)
(require 'safeslop-output)
(require 'safeslop-session)
(require 'safeslop-portal)
(require 'safeslop-install)
(require 'safeslop-profiles)

;;;###autoload
(defun safeslop-doctor (&optional callback)
  "Run `safeslop doctor --json' asynchronously and show the contract envelope.
`doctor' probes the whole toolchain, so it can take a while; the call is async so
Emacs stays responsive.  CALLBACK, when given, is called with the envelope once it
arrives (used by tests)."
  (interactive)
  (let ((args '("doctor" "--json")))
    (safeslop--call-json-async
     args
     (lambda (envelope)
       (safeslop--show-envelope-buffer "*safeslop doctor*" args envelope)
       (when callback (funcall callback envelope))))))

;;;###autoload
(defun safeslop-policy-check-file (file &optional callback)
  "Validate safeslop policy FILE asynchronously and show the contract envelope.
CALLBACK, when given, is called with the envelope once it arrives (used by tests)."
  (interactive (list (read-file-name "Policy file: " nil nil t "safeslop.cue")))
  (let ((args (list "validate" (expand-file-name file) "--json")))
    (safeslop--call-json-async
     args
     (lambda (envelope)
       (safeslop--show-envelope-buffer "*safeslop validate*" args envelope)
       (when callback (funcall callback envelope))))))

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
(defun safeslop-help ()
  "Show safeslop Emacs help."
  (interactive)
  (message "safeslop: C-c s P portal, I install, F profiles, d doctor, n new session, l list, L debug log"))

(defvar safeslop-command-map
  (let ((map (make-sparse-keymap)))
    (define-key map (kbd "P") #'safeslop-portal)
    (define-key map (kbd "I") #'safeslop-install)
    (define-key map (kbd "F") #'safeslop-profiles)
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
