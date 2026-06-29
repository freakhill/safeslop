;;; safeslop-profiles-test.el --- Tests for safeslop-profiles.el -*- lexical-binding: t; -*-

(require 'ert)
(require 'cl-lib)
(require 'safeslop)
(require 'safeslop-profiles)
(require 'safeslop-contract)

(ert-deftest safeslop-test-profiles-command-loads ()
  (should (fboundp 'safeslop-profiles))
  (should (fboundp 'safeslop-profiles-mode))
  (should (fboundp 'safeslop-profiles-create))
  (should (fboundp 'safeslop-profiles-new)) ; compatibility alias
  (should (fboundp 'safeslop-profiles-delete)))

(ert-deftest safeslop-test-profiles-rows-from-list ()
  "`safeslop-profiles--rows' builds rows from enveloped `profile list', Env coloured."
  (let* ((env (safeslop-contract-parse-string
               (concat "{\"schema_version\":1,\"ok\":true,\"data\":{"
                       "\"path\":\"/ws/safeslop.cue\",\"profiles\":{"
                       "\"review\":{\"agent\":\"claude\",\"environment\":\"container\",\"network\":\"deny\"},"
                       "\"yolo\":{\"agent\":\"pi\",\"environment\":\"host\",\"network\":\"allow\"}}},"
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
      (should (eq (get-text-property 0 'face (aref (cadr yolo) 2)) 'safeslop-tier-host)))))

(ert-deftest safeslop-test-profiles-keymap ()
  (should (eq (lookup-key safeslop-profiles-mode-map (kbd "RET")) #'safeslop-profiles-edit))
  (should (eq (lookup-key safeslop-profiles-mode-map (kbd "n")) #'safeslop-profiles-create))
  (should (eq (lookup-key safeslop-profiles-mode-map (kbd "v")) #'safeslop-profiles-validate))
  (should (eq (lookup-key safeslop-profiles-mode-map (kbd "d")) #'safeslop-profiles-delete))
  ;; inherited surface switch keys
  (should (eq (lookup-key safeslop-profiles-mode-map (kbd "P")) #'safeslop-portal))
  (should (eq (lookup-key safeslop-profiles-mode-map (kbd "I")) #'safeslop-install)))

(ert-deftest safeslop-test-profiles-catalog-names ()
  "Catalog envelopes feed bundle/package multi-select candidates by name."
  (let* ((env (safeslop-contract-parse-string
               (concat "{\"schema_version\":1,\"ok\":true,\"data\":{"
                       "\"bundles\":[{\"name\":\"base-tools\",\"description\":\"search\"},"
                       "{\"name\":\"pi\",\"description\":\"pi agent\"}],"
                       "\"packages\":[{\"name\":\"node\",\"kind\":\"binary\"},"
                       "{\"name\":\"pnpm\",\"kind\":\"npm\"}]},"
                       "\"warnings\":[],\"errors\":[]}")))
         (data (safeslop-contract-data env)))
    (should (equal (safeslop-profiles--catalog-names data 'bundles) '("base-tools" "pi")))
    (should (equal (safeslop-profiles--catalog-names data 'packages) '("node" "pnpm")))))

(ert-deftest safeslop-test-profiles-create-args-repeat-selectors ()
  "Profile create uses exact argv flags for repeated bundle/package selectors."
  (should (equal (safeslop-profiles--create-args
                  "review" "claude" "container" '("pi" "base-tools") '("pnpm")
                  "deny" "." nil)
                 '("profile" "create" "--name" "review" "--agent" "claude"
                   "--environment" "container" "--bundle" "pi" "--bundle" "base-tools"
                   "--package" "pnpm" "--workspace" "." "--network" "deny"
                   "--output" "json")))
  (should (member "--no-default-bundle"
                  (safeslop-profiles--create-args
                   "bare" "claude" "container" nil nil "deny" "" t))))

(ert-deftest safeslop-test-profiles-create-calls-cli-and-callback ()
  "Noninteractive `safeslop-profiles-create' shells out to profile create asynchronously."
  (let (seen callback-ok)
    (cl-letf (((symbol-function 'safeslop--call-json-async)
               (lambda (args cb)
                 (setq seen args)
                 (funcall cb (safeslop-contract-parse-string
                              "{\"schema_version\":1,\"ok\":true,\"data\":{\"name\":\"review\"},\"warnings\":[],\"errors\":[]}"))))
              ((symbol-function 'safeslop--show-envelope-buffer)
               (lambda (&rest _) nil)))
      (safeslop-profiles-create
       "review" "pi" "container" '("base-tools") '("pnpm") "deny" "."
       (lambda (env) (setq callback-ok (safeslop-contract-ok-p env))))
      (should (equal seen
                     '("profile" "create" "--name" "review" "--agent" "pi"
                       "--environment" "container" "--bundle" "base-tools"
                       "--package" "pnpm" "--workspace" "." "--network" "deny"
                       "--output" "json")))
      (should callback-ok))))

(ert-deftest safeslop-test-profiles-validate-needs-config ()
  (with-temp-buffer
    (safeslop-profiles-mode)
    (setq safeslop-profiles--config-path nil)
    (should-error (safeslop-profiles-validate) :type 'user-error)))

;;; safeslop-profiles-test.el ends here
