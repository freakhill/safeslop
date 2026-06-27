;;; safeslop-profiles.el --- Policy/profile surface for safeslop -*- lexical-binding: t; -*-

;; Copyright (C) 2026

;; Author: safeslop
;; Package-Requires: ((emacs "32.0"))
;; Keywords: tools, processes, ai

;;; Commentary:

;; The Profiles surface: a tabulated-list view of the profiles defined in the
;; active safeslop.cue, over `safeslop profile list --output json'.  Authoring
;; stays CUE-canonical (specs/0029): editing opens the safeslop.cue itself and
;; validates on save; creating scaffolds from a preset (`profile presets'); and
;; deleting is guided (open the file at the block, remove it, re-validate) rather
;; than a fragile machine rewrite of the guard.  All calls are async (specs/0052
;; #7).  The Env column reuses the portal's isolation-tier colouring.

;;; Code:

(require 'subr-x)
(require 'tabulated-list)
(require 'safeslop-contract)
(require 'safeslop-surface)
(require 'safeslop-portal) ; for `safeslop-portal--env-cell' (shared tier colouring)

(defvar safeslop-program)
(declare-function safeslop--call-json-async "safeslop" (args callback))
(declare-function safeslop-policy-check-file "safeslop" (file &optional callback))
(declare-function safeslop-debug-log "safeslop" ())

(defconst safeslop-profiles-buffer-name "*safeslop profiles*"
  "Buffer name for the safeslop profiles surface.")

(defvar-local safeslop-profiles--config-path nil
  "Path to the safeslop.cue backing this buffer, from the last `profile list'.
Edit/validate/delete act on this file; nil until a config is found.")

(defun safeslop-profiles--rows (data)
  "Build tabulated rows from `profile list' DATA (a profiles name->fields map).
The Env cell reuses the portal's tier colouring so isolation strength reads the
same on both surfaces."
  (mapcar
   (lambda (entry)
     (let ((name (symbol-name (car entry)))
           (p (cdr entry)))
       (list name
             (vector name
                     (or (alist-get 'agent p) "")
                     (safeslop-portal--env-cell (or (alist-get 'environment p) ""))
                     (or (alist-get 'network p) "")))))
   (alist-get 'profiles data)))

(defconst safeslop-profiles--key-hints
  '(("RET" . "edit") ("n" . "new") ("v" . "validate") ("d" . "delete")
    ("g" . "refresh") ("L" . "debug") ("?" . "help") ("q" . "quit"))
  "Key/action pairs shown in the profiles surface's in-buffer legend.")

(defun safeslop-profiles--legend ()
  "Return the profiles shortcut legend line, trailing blank line."
  (concat (mapconcat (lambda (pair)
                       (concat (propertize (car pair) 'face 'help-key-binding)
                               " " (cdr pair)))
                     safeslop-profiles--key-hints "  ")
          "\n\n"))

(defun safeslop-profiles--header ()
  "Return the profiles header block: surface tab strip then shortcut legend."
  (concat (safeslop-surface--tab-strip 'profiles)
          (safeslop-profiles--legend)))

(defun safeslop-profiles--render (&optional keep-point)
  "Asynchronously fetch the profile list, then fill the current surface buffer.
On a missing safeslop.cue the table is empty and the echo area says so; `n' still
works to scaffold one.  Stores the config path for the edit/validate/delete keys."
  (let ((buf (current-buffer)))
    (safeslop--call-json-async
     '("profile" "list" "--output" "json")
     (lambda (envelope)
       (when (buffer-live-p buf)
         (with-current-buffer buf
           (if (safeslop-contract-ok-p envelope)
               (let ((data (safeslop-contract-data envelope)))
                 (setq safeslop-profiles--config-path (alist-get 'path data))
                 (setq tabulated-list-entries (safeslop-profiles--rows data)))
             (setq tabulated-list-entries nil)
             (message "safeslop profiles: %s"
                      (or (alist-get 'message (car (safeslop-contract-errors envelope)))
                          "no safeslop.cue found — press `n' to scaffold one from a preset")))
           (tabulated-list-print keep-point)
           (let ((inhibit-read-only t))
             (save-excursion
               (goto-char (point-min))
               (insert (safeslop-profiles--header))))
           (unless keep-point
             (goto-char (point-min))
             (while (and (not (tabulated-list-get-id)) (not (eobp)))
               (forward-line 1)))))))))

(defun safeslop-profiles-refresh ()
  "Re-fetch the profile list and redraw, keeping point on its profile."
  (interactive)
  (safeslop-profiles--render t))

(defun safeslop-profiles--validate-quietly (path)
  "Validate PATH asynchronously and report ok/error in the echo area (no popup)."
  (safeslop--call-json-async
   (list "validate" (expand-file-name path) "--json")
   (lambda (env)
     (if (safeslop-contract-ok-p env)
         (message "safeslop: %s is valid" (file-name-nondirectory path))
       (message "safeslop: %s — %s" (file-name-nondirectory path)
                (or (alist-get 'message (car (safeslop-contract-errors env))) "invalid"))))))

(defun safeslop-profiles--validate-on-save ()
  "An `after-save-hook' that quietly re-validates the safeslop.cue just saved."
  (when buffer-file-name
    (safeslop-profiles--validate-quietly buffer-file-name)))

(defun safeslop-profiles--open-config (path)
  "Open PATH for editing and install the quiet validate-on-save hook."
  (find-file path)
  (add-hook 'after-save-hook #'safeslop-profiles--validate-on-save nil t))

(defun safeslop-profiles-validate ()
  "Validate the safeslop.cue backing this surface, showing the full envelope."
  (interactive)
  (if safeslop-profiles--config-path
      (safeslop-policy-check-file safeslop-profiles--config-path)
    (user-error "No safeslop.cue known; refresh, or scaffold one with `n'")))

(defun safeslop-profiles-edit ()
  "Open the active safeslop.cue for editing; saves are quietly re-validated.
CUE bytes are the source of truth (specs/0029), so editing is direct."
  (interactive)
  (let ((path safeslop-profiles--config-path))
    (unless path (user-error "No safeslop.cue known; scaffold one with `n'"))
    (safeslop-profiles--open-config path)
    (message "Editing %s — saves re-validate; `C-c s F' returns to the profiles list" path)))

(defun safeslop-profiles-delete ()
  "Open the safeslop.cue at the profile at point for guided removal.
Deletion is a guided manual edit, not a machine rewrite of the guard: removing
a CUE block by hand keeps the policy honest and avoids corrupting it (specs/0052
D5).  The save is re-validated."
  (interactive)
  (let ((name (tabulated-list-get-id))
        (path safeslop-profiles--config-path))
    (unless (and name path) (user-error "No profile/config on this line"))
    (safeslop-profiles--open-config path)
    (goto-char (point-min))
    (when (re-search-forward (concat "\\_<" (regexp-quote name) "\\_>") nil t)
      (beginning-of-line))
    (message "Remove the `%s' profile block here, then save to re-validate" name)))

(defun safeslop-profiles-new ()
  "Scaffold a new safeslop.cue from a preset, open it for editing, and validate.
Lists the embedded presets via `profile presets', writes the chosen one to a
path you pick, then opens it (CUE is canonical — you tune it from there)."
  (interactive)
  (safeslop--call-json-async
   '("profile" "presets" "--output" "json")
   (lambda (env)
     (if (not (safeslop-contract-ok-p env))
         (message "safeslop: could not list presets")
       (let* ((presets (alist-get 'presets (safeslop-contract-data env)))
              (choices (mapcar (lambda (p)
                                 (cons (format "%s — %s"
                                               (alist-get 'name p)
                                               (alist-get 'description p))
                                       p))
                               presets))
              (pick (completing-read "Preset: " choices nil t))
              (preset (cdr (assoc pick choices)))
              (cue (alist-get 'cue preset))
              (dest (read-file-name "Write safeslop.cue to: " nil "safeslop.cue")))
         (when (or (not (file-exists-p dest))
                   (yes-or-no-p (format "%s exists — overwrite? " dest)))
           (with-temp-file dest (insert cue))
           (safeslop-profiles--open-config dest)
           (safeslop-profiles--validate-quietly dest)
           (message "Scaffolded %s from a preset — tune it, then save to re-validate" dest)))))))

(defvar safeslop-profiles-mode-map
  (let ((map (make-sparse-keymap)))
    (define-key map (kbd "RET") #'safeslop-profiles-edit)
    (define-key map (kbd "e")   #'safeslop-profiles-edit)
    (define-key map (kbd "n")   #'safeslop-profiles-new)
    (define-key map (kbd "v")   #'safeslop-profiles-validate)
    (define-key map (kbd "d")   #'safeslop-profiles-delete)
    (define-key map (kbd "g")   #'safeslop-profiles-refresh)
    (define-key map (kbd "L")   #'safeslop-debug-log)
    (define-key map (kbd "?")   #'describe-mode)
    (define-key map (kbd "q")   #'quit-window)
    (set-keymap-parent map safeslop-surface-mode-map)
    map)
  "Keymap for `safeslop-profiles-mode'.")

(define-derived-mode safeslop-profiles-mode tabulated-list-mode "safeslop-profiles"
  "Major mode for the safeslop profiles (policy) surface.
\\{safeslop-profiles-mode-map}"
  (setq tabulated-list-format
        [("Profile" 20 nil)
         ("Agent" 12 nil)
         ("Env" 11 nil)
         ("Net" 6 nil)])
  (setq tabulated-list-padding 1)
  (tabulated-list-init-header))

;;;###autoload
(defun safeslop-profiles ()
  "Open the safeslop profiles surface: the profiles in your safeslop.cue.
Keys: RET/e edit, n new-from-preset, v validate, d delete (guided), g refresh;
P/I/F switch surface, [/] cycle."
  (interactive)
  (let ((buf (get-buffer-create safeslop-profiles-buffer-name)))
    (with-current-buffer buf
      (unless (derived-mode-p 'safeslop-profiles-mode)
        (safeslop-profiles-mode))
      (safeslop-profiles--render))
    (pop-to-buffer-same-window buf)
    buf))

(provide 'safeslop-profiles)
;;; safeslop-profiles.el ends here
