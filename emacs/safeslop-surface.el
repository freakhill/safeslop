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
  '((sessions "Sessions" "P" safeslop-portal)
    (install  "Install"  "I" safeslop-install)
    (profiles "Profiles" "F" safeslop-profiles))
  "Ordered surfaces: (SYMBOL LABEL KEY COMMAND).
KEY is the direct switch key shown in the tab strip (also bound in every surface
map).  Drives the tab strip and `[' / `]' / TAB cycling.  Keep in step with the
modes that set `safeslop-surface-mode-map' as their parent.")

(defun safeslop-surface--command (entry)
  "Return the surface command for an `safeslop-surface--order' ENTRY."
  (nth 3 entry))

(defun safeslop-surface--current-sym ()
  "Return the surface symbol for the current buffer's major mode, or nil."
  (cond ((derived-mode-p 'safeslop-portal-mode) 'sessions)
        ((derived-mode-p 'safeslop-install-mode) 'install)
        ((derived-mode-p 'safeslop-profiles-mode) 'profiles)))

(defun safeslop-surface--tab-strip (active)
  "Return the `Sessions | Install | Profiles' tab strip for ACTIVE surface.
Each label is preceded by its direct switch key (faced as a key binding) so the
way to change surface is legible in the strip itself, not hidden.  ACTIVE (a
surface symbol) is rendered bold via `mode-line-emphasis'; the others are
`link'-faced and clickable (mouse-1).  A trailing hint names the cycle keys.
Ends with a blank line separating it from the buffer's own shortcut legend."
  (concat
   (mapconcat
    (lambda (entry)
      (let* ((sym (car entry)) (label (cadr entry)) (key (nth 2 entry))
             (cmd (safeslop-surface--command entry))
             (switch (lambda () (interactive) (funcall cmd)))
             (keymap (let ((m (make-sparse-keymap)))
                       (define-key m [mouse-1] switch)
                       m)))
        (concat
         (propertize key 'face 'help-key-binding
                     'mouse-face 'highlight 'keymap keymap
                     'help-echo (format "Switch to the %s surface (%s)" label key))
         " "
         (if (eq sym active)
             (propertize label 'face 'mode-line-emphasis)
           (propertize label
                       'face 'link
                       'mouse-face 'highlight
                       'help-echo (format "Switch to the %s surface (%s)" label key)
                       'keymap keymap)))))
    safeslop-surface--order
    "  │  ")
   "   "
   (propertize "TAB" 'face 'help-key-binding) "/"
   (propertize "[" 'face 'help-key-binding)
   (propertize "]" 'face 'help-key-binding)
   " cycle surface"
   "\n\n"))

;; --- In-place re-render without losing the operator's place -----------------
;; safeslop's dashboards refresh by reprinting the tabulated list and re-inserting
;; the header block above it.  A naive reprint erases the buffer, which collapses
;; `window-point' to the top in every window showing the buffer that is not the
;; selected one (and drops the scroll position everywhere) — the "cursor randomly
;; jumps to the top" bug, which also makes the row action keys land on the header
;; and appear broken.  This mirrors the fix slopmaxx's console uses: snapshot each
;; showing window's start/point before the reprint and restore them after, so an
;; automatic or manual refresh never scrolls or jumps the cursor out from under
;; the operator.

(defun safeslop-surface--capture-views ()
  "Snapshot (WINDOW POINT START) for every window showing the current buffer.
Pass the result to `safeslop-surface--restore-views' after an in-place re-render."
  (mapcar (lambda (win) (list win (window-point win) (window-start win)))
          (get-buffer-window-list (current-buffer) nil t)))

(defun safeslop-surface--goto-id (id)
  "Move point to the tabulated-list row whose id is ID; return non-nil if found.
Shared by the dashboards so a keep-point refresh can re-find the operator's row
*after* the header is re-inserted (inserting the header at `point-min' otherwise
leaves a first-row point stranded on the header — part of the cursor-jump fix)."
  (when id
    (goto-char (point-min))
    (let (found)
      (while (and (not found) (not (eobp)))
        (if (equal (tabulated-list-get-id) id)
            (setq found t)
          (forward-line 1)))
      found)))

(defun safeslop-surface--restore-views (views &optional point)
  "Restore scroll and cursor for VIEWS captured by `safeslop-surface--capture-views'.
POINT, when non-nil, is the buffer position every window's cursor is synced to
(e.g. the row `tabulated-list-print' just restored); otherwise each window keeps
its own captured point.  Positions are clamped so a now-shorter buffer cannot
error, and `set-window-start' is non-forcing so the cursor stays visible."
  (dolist (view views)
    (let ((win (nth 0 view)) (old-point (nth 1 view)) (start (nth 2 view)))
      (when (window-live-p win)
        (set-window-point win (min (or point old-point) (point-max)))
        (set-window-start win (min start (point-max)) t)))))

(defun safeslop-surface--step (delta)
  "Switch to the surface DELTA positions from the current one, wrapping around."
  (let* ((syms (mapcar #'car safeslop-surface--order))
         (n (length syms))
         (cur (or (safeslop-surface--current-sym) (car syms)))
         (idx (or (seq-position syms cur) 0))
         (target (nth (mod (+ idx delta) n) syms)))
    (funcall (safeslop-surface--command (assq target safeslop-surface--order)))))

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
    (define-key map (kbd "TAB") #'safeslop-surface-next)
    (define-key map (kbd "<backtab>") #'safeslop-surface-prev)
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
