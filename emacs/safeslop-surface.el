;;; safeslop-surface.el --- Shared navigation for safeslop dashboards -*- lexical-binding: t; -*-

;; Copyright (C) 2026

;; Author: safeslop
;; Package-Requires: ((emacs "32.0"))
;; Keywords: tools, processes, ai

;;; Commentary:

;; The safeslop operator view is three sibling dashboard buffers — Sessions
;; (`safeslop-portal'), Install (`safeslop-install'), and Profiles
;; (`safeslop-profiles') — that share one navigation model (specs/0052).  This
;; file holds the bits common to all three: a parent keymap binding the surface
;; switch keys (P/I/F and [/]) and a textual tab strip rendered atop each buffer
;; so the active surface is legible without colour (specs/0031 non-colour
;; signalling).  Each surface mode sets this map as its keymap parent.

;;; Code:

(require 'seq)

(declare-function safeslop-portal "safeslop-portal" ())
(declare-function safeslop-install "safeslop-install" ())
(declare-function safeslop-profiles "safeslop-profiles" ())

(defconst safeslop-surface--order
  '((sessions "Sessions" . safeslop-portal)
    (install  "Install"  . safeslop-install)
    (profiles "Profiles" . safeslop-profiles))
  "Ordered surfaces: (SYMBOL LABEL . COMMAND).
Drives the tab strip and `[' / `]' cycling.  Keep in step with the modes that
set `safeslop-surface-mode-map' as their parent.")

(defun safeslop-surface--current-sym ()
  "Return the surface symbol for the current buffer's major mode, or nil."
  (cond ((derived-mode-p 'safeslop-portal-mode) 'sessions)
        ((derived-mode-p 'safeslop-install-mode) 'install)
        ((derived-mode-p 'safeslop-profiles-mode) 'profiles)))

(defun safeslop-surface--tab-strip (active)
  "Return the `Sessions | Install | Profiles' tab strip for ACTIVE surface.
ACTIVE (a surface symbol) is rendered bold via `mode-line-emphasis'; the others
are `link'-faced and clickable.  Ends with a blank line separating it from the
buffer's own shortcut legend."
  (concat
   (mapconcat
    (lambda (entry)
      (let ((sym (car entry)) (label (cadr entry)) (cmd (cddr entry)))
        (if (eq sym active)
            (propertize label 'face 'mode-line-emphasis)
          (propertize label
                      'face 'link
                      'mouse-face 'highlight
                      'help-echo (format "Switch to the %s surface" label)
                      'keymap (let ((m (make-sparse-keymap)))
                                (define-key m [mouse-1]
                                            (lambda () (interactive) (funcall cmd)))
                                m)))))
    safeslop-surface--order
    "  │  ")
   "\n\n"))

(defun safeslop-surface--step (delta)
  "Switch to the surface DELTA positions from the current one, wrapping around."
  (let* ((syms (mapcar #'car safeslop-surface--order))
         (n (length syms))
         (cur (or (safeslop-surface--current-sym) (car syms)))
         (idx (or (seq-position syms cur) 0))
         (target (nth (mod (+ idx delta) n) syms)))
    (funcall (cddr (assq target safeslop-surface--order)))))

(defun safeslop-surface-next ()
  "Switch to the next safeslop surface."
  (interactive)
  (safeslop-surface--step 1))

(defun safeslop-surface-prev ()
  "Switch to the previous safeslop surface."
  (interactive)
  (safeslop-surface--step -1))

(defvar safeslop-surface-mode-map
  (let ((map (make-sparse-keymap)))
    (define-key map (kbd "P") #'safeslop-portal)
    (define-key map (kbd "I") #'safeslop-install)
    (define-key map (kbd "F") #'safeslop-profiles)
    (define-key map (kbd "[") #'safeslop-surface-prev)
    (define-key map (kbd "]") #'safeslop-surface-next)
    map)
  "Parent keymap shared by every safeslop dashboard surface.
Surface modes install it with `set-keymap-parent'; their own action keys take
precedence and these switch keys fall through.")

(provide 'safeslop-surface)
;;; safeslop-surface.el ends here
