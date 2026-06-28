;;; safeslop-profiles-test.el --- Tests for safeslop-profiles.el -*- lexical-binding: t; -*-

(require 'ert)
(require 'safeslop)
(require 'safeslop-profiles)
(require 'safeslop-contract)

(ert-deftest safeslop-test-profiles-command-loads ()
  (should (fboundp 'safeslop-profiles))
  (should (fboundp 'safeslop-profiles-mode))
  (should (fboundp 'safeslop-profiles-new))
  (should (fboundp 'safeslop-profiles-delete)))

(ert-deftest safeslop-test-profiles-rows-from-list ()
  "`safeslop-profiles--rows' builds rows from enveloped `profile list', Env coloured."
  (let* ((env (safeslop-contract-parse-string
               (concat "{\"schema_version\":1,\"ok\":true,\"data\":{"
                       "\"path\":\"/ws/safeslop.cue\",\"profiles\":{"
                       "\"review\":{\"agent\":\"claude\",\"environment\":\"container\",\"network\":\"deny\"},"
                       "\"yolo\":{\"agent\":\"pi\",\"environment\":\"vm\",\"network\":\"allow\"}}},"
                       "\"warnings\":[],\"errors\":[]}")))
         (rows (safeslop-profiles--rows (safeslop-contract-data env))))
    (should (= (length rows) 2))
    (let ((review (assoc "review" rows)))
      (should review)
      (should (equal (aref (cadr review) 0) "review"))
      (should (equal (aref (cadr review) 1) "claude"))
      (should (equal (aref (cadr review) 2) "container")) ; env-cell text; equal ignores face
      (should (equal (aref (cadr review) 3) "deny"))
      (should (eq (get-text-property 0 'face (aref (cadr review) 2)) 'safeslop-tier-container)))
    (let ((yolo (assoc "yolo" rows)))
      (should (eq (get-text-property 0 'face (aref (cadr yolo) 2)) 'safeslop-tier-vm)))))

(ert-deftest safeslop-test-profiles-keymap ()
  (should (eq (lookup-key safeslop-profiles-mode-map (kbd "RET")) #'safeslop-profiles-edit))
  (should (eq (lookup-key safeslop-profiles-mode-map (kbd "n")) #'safeslop-profiles-new))
  (should (eq (lookup-key safeslop-profiles-mode-map (kbd "v")) #'safeslop-profiles-validate))
  (should (eq (lookup-key safeslop-profiles-mode-map (kbd "d")) #'safeslop-profiles-delete))
  ;; inherited surface switch keys
  (should (eq (lookup-key safeslop-profiles-mode-map (kbd "P")) #'safeslop-portal))
  (should (eq (lookup-key safeslop-profiles-mode-map (kbd "I")) #'safeslop-install)))

(ert-deftest safeslop-test-profiles-validate-needs-config ()
  (with-temp-buffer
    (safeslop-profiles-mode)
    (setq safeslop-profiles--config-path nil)
    (should-error (safeslop-profiles-validate) :type 'user-error)))

;;; safeslop-profiles-test.el ends here
