;;; safeslop-install.el --- Toolchain install/update surface for safeslop -*- lexical-binding: t; -*-

;; Copyright (C) 2026

;; Author: safeslop
;; Package-Requires: ((emacs "32.0"))
;; Keywords: tools, processes, ai

;;; Commentary:

;; The Install surface: one dashboard listing the toolchains and runtimes
;; safeslop manages, with the actions to plan, apply, and roll them back.  It is
;; a thin tabulated-list view over `safeslop install status --output json' (and
;; the plan/apply/rollback siblings), every call async through the shared
;; substrate so even a slow `install apply' download never freezes Emacs
;; (specs/0052 #6).  Rows and actions live here; the fetch/reprint machinery is
;; the shared `safeslop-surface-render' engine (specs/0062).

;;; Code:

(require 'subr-x)
(require 'tabulated-list)
(require 'safeslop-contract)
(require 'safeslop-client)
(require 'safeslop-surface)
(require 'safeslop-output)

(defconst safeslop-install-buffer-name "*safeslop install*"
  "Buffer name for the safeslop install/update surface.")

(defun safeslop-install--present-cell (present)
  "Return an install-state cell: PRESENT non-nil -> installed, else missing."
  (if present
      (propertize "installed" 'face 'success)
    (propertize "missing" 'face 'shadow)))

(defun safeslop-install--tool-rows (kind tools)
  "Build tabulated rows for TOOLS (a list of alists), each tagged KIND."
  (mapcar
   (lambda (tool)
     (let ((name (or (alist-get 'name tool) "")))
       (list name
             (vector name
                     kind
                     (or (alist-get 'version tool) "")
                     (safeslop-install--present-cell
                      (eq (alist-get 'present tool) t))))))
   tools))

(defun safeslop-install--rows (data)
  "Build tabulated rows from install status DATA (toolchains then runtimes)."
  (append
   (safeslop-install--tool-rows "toolchain" (alist-get 'toolchains data))
   (safeslop-install--tool-rows "runtime" (alist-get 'runtimes data))))

(defconst safeslop-install--key-hints
  '(("g" . "refresh") ("p" . "plan") ("x" . "apply") ("D" . "dry-run")
    ("b" . "rollback") ("d" . "doctor") ("E" . "error") ("L" . "debug")
    ("?" . "help") ("q" . "quit"))
  "Key/action pairs shown in the install surface's in-buffer legend.")

(defun safeslop-install--legend ()
  "Return the install shortcut legend line, trailing blank line."
  (safeslop-surface--legend safeslop-install--key-hints))

(defun safeslop-install--header ()
  "Return the install header block: surface tab strip then shortcut legend."
  (concat (safeslop-surface--tab-strip 'install)
          (safeslop-install--legend)))

(defun safeslop-install--render (&optional keep-point)
  "Fetch install status and redraw the current surface buffer in place.
A thin wrapper over the shared `safeslop-surface-render' engine; KEEP-POINT is
the engine's KEEP-POINT (stay on the same tool across the reprint)."
  (safeslop-surface-render
   :argv '("install" "status" "--output" "json")
   :label "install status"
   :noun "managed tools"
   :header-fn #'safeslop-install--header
   :empty-fn (lambda () (safeslop-surface--empty-state "managed tools" nil))
   :entries-fn (lambda (envelope)
                 (safeslop-install--rows (safeslop-contract-data envelope)))
   :keep-point keep-point))

(defun safeslop-install-refresh ()
  "Re-fetch install status and redraw, keeping point on its tool."
  (interactive)
  (safeslop-install--render t))

(defun safeslop-install-plan ()
  "Show the pinned install/upgrade plan in an envelope buffer."
  (interactive)
  (let ((args '("install" "plan" "--output" "json")))
    (safeslop--call-json-async
     args
     (lambda (envelope)
       (safeslop--show-envelope-buffer "*safeslop install plan*" args envelope)))))

(defun safeslop-install-dry-run ()
  "Show what `install apply' would do, without doing it."
  (interactive)
  (let ((args '("install" "apply" "--dry-run" "--output" "json")))
    (safeslop--call-json-async
     args
     (lambda (envelope)
       (safeslop--show-envelope-buffer "*safeslop install dry-run*" args envelope)))))

(defun safeslop-install-apply ()
  "Download, verify (fail-closed), and install the pinned toolchains, then refresh.
Confirmed first; the call is async so the (possibly long) download never blocks
Emacs."
  (interactive)
  (when (yes-or-no-p "Apply the pinned install/upgrade plan (downloads + installs tools)? ")
    (let ((args '("install" "apply" "--output" "json"))
          (buf (current-buffer)))
      (message "safeslop install: applying… (Emacs stays responsive; result shows when done)")
      (safeslop--call-json-async
       args
       (lambda (envelope)
         (safeslop--show-envelope-buffer "*safeslop install apply*" args envelope)
         (when (buffer-live-p buf)
           (with-current-buffer buf (safeslop-install-refresh))))))))

(defun safeslop-install-rollback ()
  "Roll the tool at point back to its prior version (a backup of the last install)."
  (interactive)
  (let ((name (tabulated-list-get-id)))
    (unless name (user-error "No tool on this line"))
    (when (yes-or-no-p (format "Roll back %s to its prior version? " name))
      (let ((args (list "install" "rollback" name "--output" "json"))
            (buf (current-buffer)))
        (safeslop--call-json-async
         args
         (lambda (envelope)
           (safeslop--show-envelope-buffer "*safeslop install rollback*" args envelope)
           (when (buffer-live-p buf)
             (with-current-buffer buf (safeslop-install-refresh)))))))))

(defvar safeslop-install-mode-map
  (let ((map (make-sparse-keymap)))
    (define-key map (kbd "g") #'safeslop-install-refresh)
    (define-key map (kbd "p") #'safeslop-install-plan)
    (define-key map (kbd "x") #'safeslop-install-apply)
    (define-key map (kbd "D") #'safeslop-install-dry-run)
    (define-key map (kbd "b") #'safeslop-install-rollback)
    ;; d/E/L/?/q and the surface switch keys fall through to the shared parent.
    (set-keymap-parent map safeslop-surface-mode-map)
    map)
  "Keymap for `safeslop-install-mode'.")

(define-derived-mode safeslop-install-mode tabulated-list-mode "safeslop-install"
  "Major mode for the safeslop toolchain install/update surface.
\\{safeslop-install-mode-map}"
  (setq tabulated-list-format
        [("Tool" 18 nil)
         ("Kind" 11 nil)
         ("Version" 16 nil)
         ("State" 12 nil)])
  (setq tabulated-list-padding 1)
  (tabulated-list-init-header))

;;;###autoload
(defun safeslop-install ()
  "Open the safeslop install/update surface: toolchains + runtimes you can act on.
Keys: g refresh, p plan, x apply, D dry-run, b rollback, d doctor; P/I/F switch
surface, [/] cycle."
  (interactive)
  (let ((buf (get-buffer-create safeslop-install-buffer-name)))
    (with-current-buffer buf
      (unless (derived-mode-p 'safeslop-install-mode)
        (safeslop-install-mode))
      (safeslop-install--render))
    (pop-to-buffer-same-window buf)
    buf))

(provide 'safeslop-install)
;;; safeslop-install.el ends here
