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
(declare-function safeslop-doctor "safeslop" ())
(declare-function safeslop-debug-log "safeslop" ())
(declare-function safeslop-show-last-error "safeslop" ())

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

(defface safeslop-net-deny '((t :inherit success))
  "Face for network=deny: default-deny egress (safe default)."
  :group 'safeslop)

(defface safeslop-net-allow '((t :inherit warning))
  "Face for network=allow: open outbound egress from the boundary."
  :group 'safeslop)

(defface safeslop-surface-error '((t :inherit error :weight bold))
  "Face for persistent safeslop surface error banners."
  :group 'safeslop)

(defface safeslop-surface-hint '((t :inherit shadow))
  "Face for persistent safeslop surface empty/loading guidance."
  :group 'safeslop)

(defun safeslop-surface--net-cell (net)
  "Return NET as a colour-redundant cell with honest egress help."
  (pcase net
    ("allow" (propertize "allow" 'face 'safeslop-net-allow
                          'help-echo "network=allow: open outbound egress from the agent boundary"))
    ("deny" (propertize "deny" 'face 'safeslop-net-deny
                         'help-echo "network=deny: default-deny egress (safe default)"))
    (_ (or net ""))))

(defun safeslop-surface--error-message (envelope &optional fallback)
  "Return ENVELOPE's first error message, or FALLBACK."
  (or (alist-get 'message (car (and (fboundp 'safeslop-contract-errors)
                                    (safeslop-contract-errors envelope))))
      fallback
      "unknown error"))

(defun safeslop-surface--error-banner (label message)
  "Return persistent error guidance for LABEL and MESSAGE."
  (concat (propertize (format "⚠ %s failed: %s" label message)
                      'face 'safeslop-surface-error)
          " · "
          (propertize "g" 'face 'help-key-binding) " retry  "
          (propertize "d" 'face 'help-key-binding) " doctor  "
          (propertize "E" 'face 'help-key-binding) " last error  "
          (propertize "L" 'face 'help-key-binding) " debug\n"))

(defun safeslop-surface--empty-state (noun new-key)
  "Return persistent empty-state guidance for NOUN, advertising NEW-KEY when non-nil."
  (concat (propertize (format "No %s yet" noun) 'face 'safeslop-surface-hint)
          (if new-key
              (format " — press %s to create one, or %s to refresh.\n"
                      (propertize new-key 'face 'help-key-binding)
                      (propertize "g" 'face 'help-key-binding))
            (format " — press %s to refresh or %s for doctor.\n"
                    (propertize "g" 'face 'help-key-binding)
                    (propertize "d" 'face 'help-key-binding)))))

(defun safeslop-surface--loading (noun)
  "Return a non-blocking loading banner for NOUN."
  (propertize (format "↻ checking %s… (Emacs stays responsive)\n" noun)
              'face 'safeslop-surface-hint))

(defun safeslop-surface--infer (args)
  "Infer the active surface symbol from safeslop ARGS."
  (pcase args
    (`("session" . ,_) 'sessions)
    (`("install" . ,_) 'install)
    (`("profile" . ,_) 'profiles)
    (`("validate" . ,_) 'profiles)
    (_ nil)))

(defun safeslop-surface--breadcrumb-title (args)
  "Return a compact title for an output buffer produced by ARGS."
  (let ((tokens nil))
    (dolist (arg args)
      (unless (or (string-prefix-p "--" arg)
                  (string-match-p "\`/" arg))
        (push arg tokens)))
    (string-join (seq-take (nreverse tokens) 2) " ")))

(defun safeslop-surface--breadcrumb (args)
  "Return a operator UI tab strip plus compact output title for ARGS."
  (let ((active (safeslop-surface--infer args))
        (title (safeslop-surface--breadcrumb-title args)))
    (concat (safeslop-surface--tab-strip active)
            (when (not (string-empty-p title))
              (format "▸ %s\n\n" title)))))

(defvar safeslop-surface-mode-map
  (let ((map (make-sparse-keymap)))
    (define-key map (kbd "P") #'safeslop-portal)
    (define-key map (kbd "I") #'safeslop-install)
    (define-key map (kbd "F") #'safeslop-profiles)
    (define-key map (kbd "[") #'safeslop-surface-prev)
    (define-key map (kbd "]") #'safeslop-surface-next)
    (define-key map (kbd "d") #'safeslop-doctor)
    (define-key map (kbd "E") #'safeslop-show-last-error)
    (define-key map (kbd "L") #'safeslop-debug-log)
    (define-key map (kbd "?") #'describe-mode)
    (define-key map (kbd "q") #'quit-window)
    map)
  "Parent keymap shared by every safeslop dashboard surface.
Surface modes install it with `set-keymap-parent'; their own action keys take
precedence and these switch keys fall through.")

(provide 'safeslop-surface)
;;; safeslop-surface.el ends here
