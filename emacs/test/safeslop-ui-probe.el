;;; safeslop-ui-probe.el --- Batch UI key-resolution probe -*- lexical-binding: t; -*-

;; This file is a local compatibility probe, not a package dependency.  It is
;; intentionally batch-friendly so ci/emacs-ui-matrix.sh can run raw Emacs,
;; Doom-shim, real Evil, Doom+Evil, and an opt-in personal config without
;; installing packages or reading private config files.

(require 'ert)
(require 'cl-lib)

(defconst safeslop-ui-probe--slot
  (or (getenv "SAFESLOP_UI_PROBE_SLOT") "raw")
  "Human slot name printed in probe failures.")

(defconst safeslop-ui-probe--evil-requested
  (member (getenv "SAFESLOP_UI_PROBE_EVIL") '("1" "true" "yes"))
  "Non-nil when the matrix slot must load and exercise Evil.")

(defconst safeslop-ui-probe--doom-shim
  (member (getenv "SAFESLOP_UI_PROBE_DOOM_SHIM") '("1" "true" "yes"))
  "Non-nil when a tiny Doom `map!' shim should be installed.")

(defvar safeslop-ui-probe--evil-active nil
  "Non-nil when key assertions should run in Evil normal state.")

(defvar safeslop-ui-probe--map-forms nil
  "Forms captured by the Doom-shim `map!' macro.")

(defun safeslop-ui-probe--record-map! (args)
  "Record Doom-shim ARGS and report success to `safeslop-doom-bind-leader'."
  (push args safeslop-ui-probe--map-forms)
  t)

(when safeslop-ui-probe--doom-shim
  ;; Keep the shim deliberately tiny: enough for safeslop-doom.el to prove it
  ;; calls Doom's leader binding hook, without pretending to be Doom or touching
  ;; real user keymaps.
  (unless (fboundp 'map!)
    (defmacro map! (&rest args)
      `(safeslop-ui-probe--record-map! ',args))))

(require 'safeslop-doom)

(when safeslop-ui-probe--evil-requested
  (unless (require 'evil nil t)
    (error "slot %s requested Evil, but `(require 'evil)' failed; set SAFESLOP_EVIL_LOAD_PATH"
           safeslop-ui-probe--slot))
  (evil-mode 1))

(setq safeslop-ui-probe--evil-active
      (and (featurep 'evil)
           (or safeslop-ui-probe--evil-requested
               (and (boundp 'evil-mode) evil-mode))))

(defun safeslop-ui-probe--tree-member-p (needle tree)
  "Return non-nil when NEEDLE appears anywhere in TREE."
  (cond ((equal needle tree) t)
        ((consp tree)
         (or (safeslop-ui-probe--tree-member-p needle (car tree))
             (safeslop-ui-probe--tree-member-p needle (cdr tree))))
        (t nil)))

(defun safeslop-ui-probe--enter-key-state ()
  "Put the current buffer in the key state this slot is probing."
  (when safeslop-ui-probe--evil-active
    (when (and (fboundp 'evil-local-mode)
               (not (bound-and-true-p evil-local-mode)))
      (evil-local-mode 1))
    (when (fboundp 'evil-normal-state)
      (evil-normal-state))
    (when (fboundp 'evil-normalize-keymaps)
      (evil-normalize-keymaps))))

(defmacro safeslop-ui-probe--with-mode (mode &rest body)
  "Run BODY in a temp buffer after enabling MODE and this slot's key state."
  (declare (indent 1) (debug t))
  `(with-temp-buffer
     (funcall ,mode)
     (safeslop-ui-probe--enter-key-state)
     ,@body))

(defun safeslop-ui-probe--assert-key (key expected)
  "Assert active KEY resolves to EXPECTED in the current buffer."
  (let ((actual (key-binding (kbd key))))
    (unless (eq actual expected)
      (ert-fail
       (format "slot=%s mode=%s key=%s resolved to %S, expected %S"
               safeslop-ui-probe--slot major-mode key actual expected)))))

(ert-deftest safeslop-ui-probe-loads-package ()
  "The Emacs package and optional Doom shim load in this slot."
  (should (featurep 'safeslop))
  (should (featurep 'safeslop-doom))
  (dolist (fn '(safeslop-portal safeslop-profiles safeslop-credentials
                safeslop-profiles-compose-toggle safeslop-doom-bind-leader))
    (should (fboundp fn))))

(ert-deftest safeslop-ui-probe-doom-shim-leader ()
  "When the matrix provides `map!', the Doom leader hook calls it."
  (unless safeslop-ui-probe--doom-shim
    (ert-skip "Doom-shim slot not requested"))
  (setq safeslop-ui-probe--map-forms nil)
  (should (eq (safeslop-doom-bind-leader) t))
  (should safeslop-ui-probe--map-forms)
  (should (safeslop-ui-probe--tree-member-p 'safeslop-command-map
                                            safeslop-ui-probe--map-forms))
  (should (safeslop-ui-probe--tree-member-p "s" safeslop-ui-probe--map-forms)))

(ert-deftest safeslop-ui-probe-core-surface-keys-resolve ()
  "Core safeslop surfaces expose their documented active keys in this slot."
  (if safeslop-ui-probe--evil-active
      (progn
        (safeslop-ui-probe--with-mode #'safeslop-output-mode
          (safeslop-ui-probe--assert-key "gr" #'safeslop-output-refresh)
          (safeslop-ui-probe--assert-key "E" #'safeslop-show-last-error)
          (safeslop-ui-probe--assert-key "q" #'quit-window))
        (safeslop-ui-probe--with-mode #'safeslop-portal-mode
          (safeslop-ui-probe--assert-key "RET" #'safeslop-portal-open)
          (safeslop-ui-probe--assert-key "gr" #'safeslop-portal-refresh)
          (safeslop-ui-probe--assert-key "ga" #'safeslop-portal-toggle-auto-refresh)
          (safeslop-ui-probe--assert-key "F" #'safeslop-profiles))
        (safeslop-ui-probe--with-mode #'safeslop-profiles-mode
          (safeslop-ui-probe--assert-key "RET" #'safeslop-profiles-inspect)
          (safeslop-ui-probe--assert-key "gr" #'safeslop-profiles-refresh)
          (safeslop-ui-probe--assert-key "K" #'safeslop-credentials))
        (safeslop-ui-probe--with-mode #'safeslop-credentials-mode
          (safeslop-ui-probe--assert-key "RET" #'safeslop-credentials-inspect)
          (safeslop-ui-probe--assert-key "A" #'safeslop-credentials-link-account)
          (safeslop-ui-probe--assert-key "U" #'safeslop-credentials-unlink-account)
          (safeslop-ui-probe--assert-key "R" #'safeslop-credentials-pick-repositories)
          (safeslop-ui-probe--assert-key "X" #'safeslop-credentials-clear-profile-forge)
          (safeslop-ui-probe--assert-key "gr" #'safeslop-credentials-refresh)
          (should (string-match-p "gr refresh" (substring-no-properties (safeslop-credentials--header))))
          (safeslop-ui-probe--assert-key "P" #'safeslop-portal)))
    (safeslop-ui-probe--with-mode #'safeslop-output-mode
      (safeslop-ui-probe--assert-key "g" #'safeslop-output-refresh)
      (safeslop-ui-probe--assert-key "E" #'safeslop-show-last-error)
      (safeslop-ui-probe--assert-key "q" #'quit-window))
    (safeslop-ui-probe--with-mode #'safeslop-portal-mode
      (safeslop-ui-probe--assert-key "RET" #'safeslop-portal-open)
      (safeslop-ui-probe--assert-key "g" #'safeslop-portal-refresh)
      (safeslop-ui-probe--assert-key "a" #'safeslop-portal-toggle-auto-refresh)
      (safeslop-ui-probe--assert-key "F" #'safeslop-profiles))
    (safeslop-ui-probe--with-mode #'safeslop-profiles-mode
      (safeslop-ui-probe--assert-key "RET" #'safeslop-profiles-inspect)
      (safeslop-ui-probe--assert-key "g" #'safeslop-profiles-refresh)
      (safeslop-ui-probe--assert-key "K" #'safeslop-credentials))
    (safeslop-ui-probe--with-mode #'safeslop-credentials-mode
      (safeslop-ui-probe--assert-key "RET" #'safeslop-credentials-inspect)
      (safeslop-ui-probe--assert-key "A" #'safeslop-credentials-link-account)
      (safeslop-ui-probe--assert-key "U" #'safeslop-credentials-unlink-account)
      (safeslop-ui-probe--assert-key "R" #'safeslop-credentials-pick-repositories)
      (safeslop-ui-probe--assert-key "X" #'safeslop-credentials-clear-profile-forge)
      (safeslop-ui-probe--assert-key "g" #'safeslop-credentials-refresh)
      (should (string-match-p "g refresh" (substring-no-properties (safeslop-credentials--header))))
      (safeslop-ui-probe--assert-key "P" #'safeslop-portal))))

(ert-deftest safeslop-ui-probe-compose-keys-resolve ()
  "Profiles compose `RET' resolves to checkbox toggle; `SPC' is not used."
  (safeslop-ui-probe--with-mode #'safeslop-profiles-compose-mode
    (safeslop-ui-probe--assert-key "RET" #'safeslop-profiles-compose-toggle)
    (when (eq (key-binding (kbd "SPC")) #'safeslop-profiles-compose-toggle)
      (ert-fail (format "slot=%s mode=%s key=SPC still toggles compose rows"
                        safeslop-ui-probe--slot major-mode)))
    (safeslop-ui-probe--assert-key "?" #'safeslop-profiles-compose-help)
    (safeslop-ui-probe--assert-key (if safeslop-ui-probe--evil-active "gr" "g")
                                   #'safeslop-profiles-compose-refresh)
    (safeslop-ui-probe--assert-key "C-c C-c" #'safeslop-profiles-compose-preview-save)
    (safeslop-ui-probe--assert-key "q" #'safeslop-profiles-compose-cancel)))

(provide 'safeslop-ui-probe)
;;; safeslop-ui-probe.el ends here
