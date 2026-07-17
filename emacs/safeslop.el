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
;;   safeslop-contract.el          versioned JSON envelope parse/validate
;;   safeslop-client.el            CLI subprocess substrate + redacted debug log
;;   safeslop-surface.el           shared dashboard chrome + the render engine
;;   safeslop-output.el            read-only envelope output buffers
;;   safeslop-session-terminal.el  session PTY launch, monitor, failure chrome
;;   safeslop-egress.el            progressive session egress command + review UI
;;   safeslop-session.el           session front: commands, attach, detail view
;;   safeslop-portal.el            Sessions dashboard
;;   safeslop-profile-evaluation.el  profile safety evaluation validate/render
;;   safeslop-profile-compose.el     interactive profile composition UI
;;   safeslop-profiles.el          Profiles front: dashboard + CUE-backed CRUD
;;   safeslop-credentials.el       Credentials posture (value-free readiness)
;;   safeslop.el                   this file: top-level commands + `C-c s' map
;;
;; safeslop-egress.el and safeslop-session-terminal.el are private feature shards
;; of the session front: they own `safeslop-session--*' internals and are
;; required by safeslop-session.el, which is the only public entry to them.
;; safeslop-profile-evaluation.el and safeslop-profile-compose.el are the private
;; shards of the profile front (safeslop-profiles.el).  Doom/Evil integration
;; stays optional in safeslop-doom.el, which requires only this entry file.

;;; Code:

(require 'safeslop-contract)
(require 'safeslop-client)
(require 'safeslop-surface)
(require 'safeslop-output)
(require 'safeslop-session)
(require 'safeslop-portal)
(require 'safeslop-profiles)
(require 'safeslop-credentials)

;;;###autoload
(defun safeslop-doctor (&optional callback)
  "Run `safeslop doctor --json' asynchronously and show the contract
envelope. `doctor' probes the whole toolchain, so it can take a while;
the call is async so Emacs stays responsive.  CALLBACK, when given, is
called with the envelope once it arrives (used by tests)."
  (interactive)
  (let ((args '("doctor" "--json")))
    (safeslop--call-json-async
     args
     (lambda (envelope)
       (safeslop--show-envelope-buffer "*safeslop doctor*" args envelope)
       (when callback (funcall callback envelope))))))

;;;###autoload
(defun safeslop-policy-check-file (file &optional callback)
  "Validate safeslop policy FILE asynchronously and show the contract
envelope. CALLBACK, when given, is called with the envelope once it
arrives (used by tests)."
  (interactive (list (read-file-name "Policy file: " nil nil t "safeslop.cue")))
  (let ((args (list "validate" (expand-file-name file) "--json")))
    (safeslop--call-json-async
     args
     (lambda (envelope)
       (safeslop--show-envelope-buffer "*safeslop validate*" args envelope)
       (when callback (funcall callback envelope))))))

(defun safeslop--buffers ()
  "Return live safeslop buffers, most recently used first.
Matches the `*safeslop…*' naming family shared by dashboards, output/result
buffers, session terminals, detail/inspect views, progress, and the debug log."
  (seq-filter (lambda (b) (string-match-p "\\`\\*safeslop" (buffer-name b)))
              (buffer-list)))

;;;###autoload
(defun safeslop-switch-to-session-buffer ()
  "Switch to a live safeslop buffer, most recently used first.
Offers every safeslop buffer family (specs/0063 F8), not just doctor/validate."
  (interactive)
  (let ((names (mapcar #'buffer-name (safeslop--buffers))))
    (if names
        (pop-to-buffer (completing-read "safeslop buffer: " names nil t nil nil
                                        (car names)))
      (message "No safeslop buffer yet — C-c s P opens the portal"))))

;;;###autoload
(defun safeslop-help ()
  "Show safeslop Emacs help, generated from the real `C-c s' command map.
Generated rather than hand-written so it cannot drift from the bindings again
(specs/0063 F8)."
  (interactive)
  (let (pairs)
    (map-keymap
     (lambda (event def)
       (when (and (symbolp def) (commandp def))
         (push (format "%s %s"
                       (key-description (vector event))
                       (replace-regexp-in-string "\\`safeslop-" ""
                                                 (symbol-name def)))
               pairs)))
     safeslop-command-map)
    (message "safeslop: C-c s %s" (string-join (nreverse pairs) ", "))))

(defvar safeslop-command-map
  (let ((map (make-sparse-keymap)))
    (define-key map (kbd "P") #'safeslop-portal)
    (define-key map (kbd "F") #'safeslop-profiles)
    (define-key map (kbd "K") #'safeslop-credentials)
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
