;;; safeslop-doom.el --- Optional Doom Emacs integration for safeslop -*- lexical-binding: t; -*-

;; Package-Requires: ((emacs "32.0"))

;;; Commentary:

;; Optional Doom Emacs sugar for safeslop.  This file does not depend on Doom:
;; it loads in raw Emacs and only binds Doom leader keys when `map!' exists.

;;; Code:

;;;###autoload (autoload 'safeslop-portal "safeslop" nil t)
;;;###autoload (autoload 'safeslop-debug-log "safeslop" nil t)
;;;###autoload (autoload 'safeslop-doctor "safeslop" nil t)
;;;###autoload (autoload 'safeslop-policy-check-file "safeslop" nil t)
;;;###autoload (autoload 'safeslop-session-new "safeslop" nil t)
;;;###autoload (autoload 'safeslop-session-attach "safeslop" nil t)
;;;###autoload (autoload 'safeslop-session-list "safeslop" nil t)
;;;###autoload (autoload 'safeslop-session-status "safeslop" nil t)
;;;###autoload (autoload 'safeslop-session-stop "safeslop" nil t)
;;;###autoload (autoload 'safeslop-session-reattach "safeslop" nil t)
;;;###autoload (autoload 'safeslop-switch-to-session-buffer "safeslop" nil t)
;;;###autoload (autoload 'safeslop-show-last-error "safeslop" nil t)
;;;###autoload (autoload 'safeslop-help "safeslop" nil t)
;;;###autoload (autoload 'safeslop-install "safeslop" nil t)
;;;###autoload (autoload 'safeslop-profiles "safeslop" nil t)

(require 'safeslop)

(declare-function evil-set-initial-state "ext:evil-states" (mode state))
(declare-function evil-define-key "ext:evil-core" (state keymap key def &rest bindings))
(declare-function safeslop-portal-open "safeslop-portal" ())
(declare-function safeslop-portal-reattach "safeslop-portal" ())
(declare-function safeslop-portal-status "safeslop-portal" ())
(declare-function safeslop-portal-stop "safeslop-portal" ())
(declare-function safeslop-portal-new "safeslop-portal" ())
(declare-function safeslop-portal-refresh "safeslop-portal" ())
(declare-function safeslop-portal-toggle-auto-refresh "safeslop-portal" ())
(declare-function safeslop-surface-next "safeslop-surface" ())
(declare-function safeslop-surface-prev "safeslop-surface" ())
(declare-function safeslop-install-refresh "safeslop-install" ())
(declare-function safeslop-install-plan "safeslop-install" ())
(declare-function safeslop-install-apply "safeslop-install" ())
(declare-function safeslop-install-dry-run "safeslop-install" ())
(declare-function safeslop-install-rollback "safeslop-install" ())
(declare-function safeslop-profiles-edit "safeslop-profiles" ())
(declare-function safeslop-profiles-new "safeslop-profiles" ())
(declare-function safeslop-profiles-validate "safeslop-profiles" ())
(declare-function safeslop-profiles-delete "safeslop-profiles" ())
(declare-function safeslop-profiles-refresh "safeslop-profiles" ())
(defvar safeslop-portal-mode-map)
(defvar safeslop-install-mode-map)
(defvar safeslop-profiles-mode-map)

(with-eval-after-load 'evil
  ;; `safeslop-output-mode' buffers are read-only command output buffers.  In
  ;; Doom/Evil, make that explicit and install bindings through Evil's normal
  ;; state so single-key actions are not interpreted as editing commands.
  (evil-set-initial-state 'safeslop-output-mode 'normal)
  (evil-define-key 'normal safeslop-output-mode-map
    (kbd "g") #'safeslop-doctor
    (kbd "e") #'safeslop-show-last-error
    (kbd "q") #'quit-window)
  ;; The portal is a tabulated-list dashboard whose single-key actions (o/i/k/n/R…)
  ;; would otherwise be Evil normal-state motions; bind them through Evil so the
  ;; dashboard is drivable.
  (evil-set-initial-state 'safeslop-portal-mode 'normal)
  (evil-define-key 'normal safeslop-portal-mode-map
    (kbd "RET") #'safeslop-portal-open
    (kbd "o")   #'safeslop-portal-open
    (kbd "R")   #'safeslop-portal-reattach
    (kbd "i")   #'safeslop-portal-status
    (kbd "k")   #'safeslop-portal-stop
    (kbd "n")   #'safeslop-portal-new
    (kbd "g")   #'safeslop-portal-refresh
    (kbd "a")   #'safeslop-portal-toggle-auto-refresh
    (kbd "d")   #'safeslop-doctor
    (kbd "L")   #'safeslop-debug-log
    (kbd "?")   #'describe-mode
    (kbd "q")   #'quit-window
    ;; Shared surface switch keys (Evil does not consult the keymap parent).
    (kbd "P")   #'safeslop-portal
    (kbd "I")   #'safeslop-install
    (kbd "F")   #'safeslop-profiles
    (kbd "[")   #'safeslop-surface-prev
    (kbd "]")   #'safeslop-surface-next)
  ;; The Install + Profiles surfaces are tabulated-list dashboards too; drive
  ;; their single-key actions and the shared switch keys through Evil normal state.
  (evil-set-initial-state 'safeslop-install-mode 'normal)
  (evil-define-key 'normal safeslop-install-mode-map
    (kbd "g") #'safeslop-install-refresh
    (kbd "p") #'safeslop-install-plan
    (kbd "x") #'safeslop-install-apply
    (kbd "D") #'safeslop-install-dry-run
    (kbd "b") #'safeslop-install-rollback
    (kbd "d") #'safeslop-doctor
    (kbd "L") #'safeslop-debug-log
    (kbd "?") #'describe-mode
    (kbd "q") #'quit-window
    (kbd "P") #'safeslop-portal
    (kbd "I") #'safeslop-install
    (kbd "F") #'safeslop-profiles
    (kbd "[") #'safeslop-surface-prev
    (kbd "]") #'safeslop-surface-next)
  (evil-set-initial-state 'safeslop-profiles-mode 'normal)
  (evil-define-key 'normal safeslop-profiles-mode-map
    (kbd "RET") #'safeslop-profiles-edit
    (kbd "e")   #'safeslop-profiles-edit
    (kbd "n")   #'safeslop-profiles-new
    (kbd "v")   #'safeslop-profiles-validate
    (kbd "d")   #'safeslop-profiles-delete
    (kbd "g")   #'safeslop-profiles-refresh
    (kbd "L")   #'safeslop-debug-log
    (kbd "?")   #'describe-mode
    (kbd "q")   #'quit-window
    (kbd "P")   #'safeslop-portal
    (kbd "I")   #'safeslop-install
    (kbd "F")   #'safeslop-profiles
    (kbd "[")   #'safeslop-surface-prev
    (kbd "]")   #'safeslop-surface-next))

;;;###autoload
(defun safeslop-doom-bind-leader ()
  "Bind `safeslop-command-map' under Doom's leader at SPC o s when Doom is present.
A no-op outside Doom Emacs.  Call this from Doom `config.el'.

This deliberately takes over SPC o s, which Doom's `:os macos' module otherwise
binds to its \"send to application\" prefix (Transmit/Launchbar/iTerm) — a chosen
override, not an accident; keep it on s (slopmaxx sits at SPC o m).  Rebind here
if you want the macOS prefix back."
  (interactive)
  (when (fboundp 'map!)
    (eval '(map! :leader
                 (:prefix ("o" . "open")
                  :desc "safeslop" "s" #'safeslop-command-map))
          t)
    t))

(provide 'safeslop-doom)
;;; safeslop-doom.el ends here
