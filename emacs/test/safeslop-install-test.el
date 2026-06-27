;;; safeslop-install-test.el --- Tests for safeslop-install.el -*- lexical-binding: t; -*-

(require 'ert)
(require 'safeslop)
(require 'safeslop-install)
(require 'safeslop-contract)

(ert-deftest safeslop-test-install-command-loads ()
  (should (fboundp 'safeslop-install))
  (should (fboundp 'safeslop-install-mode))
  (should (fboundp 'safeslop-install-apply))
  (should (fboundp 'safeslop-install-rollback)))

(ert-deftest safeslop-test-install-rows-from-status ()
  "`safeslop-install--rows' builds toolchain + runtime rows from enveloped status."
  (let* ((env (safeslop-contract-parse-string
               (concat "{\"schema_version\":1,\"ok\":true,\"data\":{"
                       "\"self\":{\"version\":\"v1\",\"on_path\":true},"
                       "\"toolchains\":[{\"name\":\"mise\",\"present\":true,\"version\":\"2026.1\"}],"
                       "\"runtimes\":[{\"name\":\"docker\",\"present\":false}]},"
                       "\"warnings\":[],\"errors\":[]}")))
         (rows (safeslop-install--rows (safeslop-contract-data env))))
    (should (= (length rows) 2))
    (let ((mise (car rows)))
      (should (equal (car mise) "mise"))
      (should (equal (aref (cadr mise) 0) "mise"))
      (should (equal (aref (cadr mise) 1) "toolchain"))
      (should (equal (aref (cadr mise) 2) "2026.1"))
      (should (equal (aref (cadr mise) 3) "installed"))) ; equal ignores face
    (let ((docker (cadr rows)))
      (should (equal (car docker) "docker"))
      (should (equal (aref (cadr docker) 1) "runtime"))
      (should (equal (aref (cadr docker) 3) "missing")))))

(ert-deftest safeslop-test-install-present-cell ()
  (should (eq (get-text-property 0 'face (safeslop-install--present-cell t)) 'success))
  (should (eq (get-text-property 0 'face (safeslop-install--present-cell nil)) 'shadow)))

(ert-deftest safeslop-test-install-keymap ()
  (should (eq (lookup-key safeslop-install-mode-map (kbd "x")) #'safeslop-install-apply))
  (should (eq (lookup-key safeslop-install-mode-map (kbd "b")) #'safeslop-install-rollback))
  (should (eq (lookup-key safeslop-install-mode-map (kbd "g")) #'safeslop-install-refresh))
  ;; inherited surface switch keys
  (should (eq (lookup-key safeslop-install-mode-map (kbd "P")) #'safeslop-portal))
  (should (eq (lookup-key safeslop-install-mode-map (kbd "F")) #'safeslop-profiles)))

;;; safeslop-install-test.el ends here
