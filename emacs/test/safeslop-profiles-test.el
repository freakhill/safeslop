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
  (should (fboundp 'safeslop-profiles-clone))
  (should (fboundp 'safeslop-profiles-inspect))
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
      (should (eq (get-text-property 0 'face (aref (cadr review) 2)) 'safeslop-tier-container))
      (should (eq (get-text-property 0 'face (aref (cadr review) 3)) 'safeslop-net-deny)))
    (let ((yolo (assoc "yolo" rows)))
      (should (eq (get-text-property 0 'face (aref (cadr yolo) 2)) 'safeslop-tier-host))
      (should (eq (get-text-property 0 'face (aref (cadr yolo) 3)) 'safeslop-net-allow)))))

(ert-deftest safeslop-test-profiles-keymap ()
  ;; RET is the safe primary action (inspect); editing the CUE file is `e'.
  (should (eq (lookup-key safeslop-profiles-mode-map (kbd "RET")) #'safeslop-profiles-inspect))
  (should (eq (lookup-key safeslop-profiles-mode-map (kbd "i")) #'safeslop-profiles-inspect))
  (should (eq (lookup-key safeslop-profiles-mode-map (kbd "e")) #'safeslop-profiles-edit))
  (should (eq (lookup-key safeslop-profiles-mode-map (kbd "n")) #'safeslop-profiles-create))
  (should (eq (lookup-key safeslop-profiles-mode-map (kbd "c")) #'safeslop-profiles-clone))
  (should (eq (lookup-key safeslop-profiles-mode-map (kbd "v")) #'safeslop-profiles-validate))
  (should (eq (lookup-key safeslop-profiles-mode-map (kbd "x")) #'safeslop-profiles-launch))
  (should (eq (lookup-key safeslop-profiles-mode-map (kbd "D")) #'safeslop-profiles-delete))
  (should (eq (lookup-key safeslop-profiles-mode-map (kbd "d")) #'safeslop-doctor))
  (should-not (eq (lookup-key safeslop-profiles-mode-map (kbd "S")) #'tabulated-list-sort))
  ;; inherited surface switch keys
  (should (eq (lookup-key safeslop-profiles-mode-map (kbd "P")) #'safeslop-portal))
  (should (eq (lookup-key safeslop-profiles-mode-map (kbd "I")) #'safeslop-install)))

(ert-deftest safeslop-test-profiles-valid-name-p ()
  "Name validation accepts CUE-ish identifiers and rejects empty/space/leading-digit."
  (should (safeslop-profiles--valid-name-p "review"))
  (should (safeslop-profiles--valid-name-p "review-strict_2"))
  (should (safeslop-profiles--valid-name-p "_scratch"))
  (should-not (safeslop-profiles--valid-name-p ""))
  (should-not (safeslop-profiles--valid-name-p "2fast"))
  (should-not (safeslop-profiles--valid-name-p "has space"))
  (should-not (safeslop-profiles--valid-name-p "weird!")))

(ert-deftest safeslop-test-profiles-show-args-use-known-config-path ()
  "Inspect/clone use the listed safeslop.cue even after cwd changes."
  (with-temp-buffer
    (safeslop-profiles-mode)
    (setq safeslop-profiles--config-path "/repo/safeslop.cue")
    (should (equal (safeslop-profiles--show-args "review")
                   '("profile" "show" "review" "/repo/safeslop.cue" "--output" "json"))))
  (with-temp-buffer
    (safeslop-profiles-mode)
    (setq safeslop-profiles--config-path nil)
    (should (equal (safeslop-profiles--show-args "review")
                   '("profile" "show" "review" "--output" "json")))))

(ert-deftest safeslop-test-profiles-copy-name-avoids-existing-clones ()
  "Clone defaults advance to a free NAME-copy-N instead of prompting overwrite first."
  (should (equal (safeslop-profiles--copy-name "review" '("review")) "review-copy"))
  (should (equal (safeslop-profiles--copy-name "review" '("review" "review-copy")) "review-copy-2"))
  (should (equal (safeslop-profiles--copy-name "review" '("review" "review-copy" "review-copy-2"))
                 "review-copy-3")))

(ert-deftest safeslop-test-profiles-normalize-workspace-allows-empty ()
  "Workspace prompt can really omit --workspace, while `.' stays the repo-root spelling."
  (should (equal (safeslop-profiles--normalize-workspace "") ""))
  (should (equal (safeslop-profiles--normalize-workspace "   ") ""))
  (should (equal (safeslop-profiles--normalize-workspace ".") ".")))

(ert-deftest safeslop-test-profiles-create-summary-labels-create-vs-update ()
  "Final confirmation says whether the UI is creating or updating before the CLI upsert."
  (cl-letf (((symbol-function 'yes-or-no-p)
             (lambda (prompt)
               (should (string-match-p "Update profile `review'" prompt))
               (should (string-match-p "workspace=\." prompt))
               t)))
    (should (safeslop-profiles--confirm-create
             '("review") "review" "claude" "container" '("web") '("pnpm") "deny" "." nil))))

(ert-deftest safeslop-test-profiles-block-anchor-regexp ()
  "The block anchor matches the field-opening brace, not loose word hits."
  (let ((re (safeslop-profiles--block-anchor-regexp "review")))
    (should (string-match-p re "\treview: {"))
    (should (string-match-p re "  \"review\": {"))
    ;; a comment, a string value, and a bundle name that merely contain the word
    ;; must NOT be mistaken for the block opener.
    (should-not (string-match-p re "// review is the default profile"))
    (should-not (string-match-p re "\tbundles: [\"review\"]"))
    (should-not (string-match-p re "\tworkspace: \"review\""))))

(ert-deftest safeslop-test-profiles-goto-profile-block ()
  "`--goto-profile-block' scopes navigation to the profiles field and fails when absent."
  (with-temp-buffer
    (insert "package safeslop\n\n"
            "review: {not: \"a profile\"}\n\n"
            "safeslop: profiles: {\n"
            "\t// review comes first\n"
            "\treview: {agent: \"claude\", bundles: [\"reviewers\"]}\n"
            "\tyolo: {agent: \"pi\"}\n}\n")
    (should (safeslop-profiles--goto-profile-block "review"))
    (should (string-match-p "review: {agent" (buffer-substring (point) (line-end-position))))
    (should (safeslop-profiles--goto-profile-block "yolo"))
    (should (string-match-p "yolo: {" (buffer-substring (point) (line-end-position))))
    (should-not (safeslop-profiles--goto-profile-block "ghost")))
  (with-temp-buffer
    (insert "package safeslop\nsafeslop: profiles: review: {agent: \"claude\"}\n")
    (should (safeslop-profiles--goto-profile-block "review"))
    (should (looking-at-p "review: {"))))

(ert-deftest safeslop-test-profiles-inspect-format ()
  "The inspect formatter renders resolved packages, egress, and recipe lines."
  (let* ((env (safeslop-contract-parse-string
               (concat "{\"schema_version\":1,\"ok\":true,\"data\":{"
                       "\"name\":\"review\","
                       "\"profile\":{\"agent\":\"claude\",\"environment\":\"container\","
                       "\"network\":\"deny\",\"workspace\":\".\",\"bundles\":[\"node\"],\"packages\":[\"pnpm\"]},"
                       "\"resolved\":{\"identitySet\":[\"claude-code\",\"node\",\"pnpm\"],"
                       "\"runtimeEgress\":[\".anthropic.com\"]},"
                       "\"recipeID\":\"abcdef012345\",\"image\":\"local/safeslop-tools:abcdef012345\","
                       "\"base\":\"debian:bookworm-slim@sha256:dead\"},"
                       "\"warnings\":[],\"errors\":[]}")))
         (text (safeslop-profiles--inspect-format (safeslop-contract-data env))))
    (should (string-match-p "Agent:       claude" text))
    (should (string-match-p "Resolved:    claude-code, node, pnpm" text))
    (should (string-match-p "Egress:      .anthropic.com" text))
    (should (string-match-p "Recipe:      abcdef012345" text))
    (should (string-match-p "Isolation:   container" text))
    (should (string-match-p "built on first launch" text))
    (should (string-match-p "Base:        debian:bookworm-slim@sha256:dead" text))))

(ert-deftest safeslop-test-profiles-clone-prefills-create-from-show ()
  "Clone reads the row's full `profile show' data and calls create with it."
  (let (create-args)
    (cl-letf (((symbol-function 'tabulated-list-get-id) (lambda () "review"))
              ((symbol-function 'safeslop-profiles--names) (lambda () '("review" "review-copy")))
              ((symbol-function 'safeslop-profiles--read-name)
               (lambda (_existing &optional default) (or default "review-copy-2")))
              ((symbol-function 'safeslop--call-json-async)
               (lambda (args cb)
                 (should (equal args '("profile" "show" "review" "/repo/safeslop.cue" "--output" "json")))
                 (funcall cb (safeslop-contract-parse-string
                              (concat "{\"schema_version\":1,\"ok\":true,\"data\":{"
                                      "\"profile\":{\"agent\":\"pi\",\"environment\":\"container\","
                                      "\"network\":\"allow\",\"workspace\":\".\",\"bundles\":[\"node\"],"
                                      "\"packages\":[\"pnpm\"],\"bareAgent\":true}},"
                                      "\"warnings\":[],\"errors\":[]}")))))
              ((symbol-function 'safeslop-profiles--confirm-create)
               (lambda (&rest _) t))
              ((symbol-function 'safeslop-profiles-create)
               (lambda (&rest args) (setq create-args args))))
      (with-temp-buffer
        (safeslop-profiles-mode)
        (setq safeslop-profiles--config-path "/repo/safeslop.cue")
        (safeslop-profiles-clone))
      (should (equal (nth 0 create-args) "review-copy-2"))
      (should (equal (nth 1 create-args) "pi"))         ; agent copied
      (should (equal (nth 2 create-args) "container"))  ; environment copied
      (should (equal (nth 3 create-args) '("node")))    ; bundles copied
      (should (equal (nth 4 create-args) '("pnpm")))    ; packages copied
      (should (equal (nth 5 create-args) "allow"))      ; network copied
      (should (eq (nth 8 create-args) t)))))            ; bareAgent copied

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
