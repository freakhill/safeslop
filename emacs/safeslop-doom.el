;;; safeslop-doom.el --- Optional Doom Emacs integration for safeslop -*- lexical-binding: t; -*-

;; Package-Requires: ((emacs "32.0"))

;;; Commentary:

;; Optional Doom Emacs sugar for safeslop.  This file does not depend on Doom:
;; it loads in raw Emacs and only binds Doom leader keys when `map!' exists.

;;; Code:

;;;###autoload (autoload 'safeslop-daemon-start "safeslop" nil t)
;;;###autoload (autoload 'safeslop-doctor "safeslop" nil t)
;;;###autoload (autoload 'safeslop-policy-check-file "safeslop" nil t)
;;;###autoload (autoload 'safeslop-session-new "safeslop" nil t)
;;;###autoload (autoload 'safeslop-session-attach "safeslop" nil t)
;;;###autoload (autoload 'safeslop-session-list "safeslop" nil t)
;;;###autoload (autoload 'safeslop-session-status "safeslop" nil t)
;;;###autoload (autoload 'safeslop-session-stop "safeslop" nil t)
;;;###autoload (autoload 'safeslop-session-restart "safeslop" nil t)
;;;###autoload (autoload 'safeslop-switch-to-session-buffer "safeslop" nil t)
;;;###autoload (autoload 'safeslop-show-last-error "safeslop" nil t)
;;;###autoload (autoload 'safeslop-help "safeslop" nil t)

(require 'safeslop)

(declare-function evil-set-initial-state "ext:evil-states" (mode state))
(declare-function evil-define-key "ext:evil-core" (state keymap key def &rest bindings))

(with-eval-after-load 'evil
  ;; `safeslop-output-mode' buffers are read-only command output buffers.  In
  ;; Doom/Evil, make that explicit and install bindings through Evil's normal
  ;; state so single-key actions are not interpreted as editing commands.
  (evil-set-initial-state 'safeslop-output-mode 'normal)
  (evil-define-key 'normal safeslop-output-mode-map
    (kbd "g") #'safeslop-doctor
    (kbd "e") #'safeslop-show-last-error
    (kbd "q") #'quit-window))

;;;###autoload
(defun safeslop-doom-bind-leader ()
  "Bind `safeslop-command-map' under Doom's leader at SPC o s when Doom is present.
A no-op outside Doom Emacs.  Call this from Doom `config.el'."
  (interactive)
  (when (fboundp 'map!)
    (eval '(map! :leader
                 (:prefix ("o" . "open")
                  :desc "safeslop" "s" #'safeslop-command-map))
          t)
    t))

(provide 'safeslop-doom)
;;; safeslop-doom.el ends here
