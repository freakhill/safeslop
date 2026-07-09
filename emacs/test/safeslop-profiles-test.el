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
  (should (eq (lookup-key safeslop-profiles-mode-map (kbd "c")) #'safeslop-profiles-create))
  (should (eq (lookup-key safeslop-profiles-mode-map (kbd "C")) #'safeslop-profiles-clone))
  (should (eq (lookup-key safeslop-profiles-mode-map (kbd "v")) #'safeslop-profiles-validate))
  (should (eq (lookup-key safeslop-profiles-mode-map (kbd "r")) #'safeslop-profiles-launch))
  ;; specs/0063 F2: launch left `x' and create left `n', so cross-surface
  ;; muscle memory can't fire the wrong risk class.
  (should-not (lookup-key safeslop-profiles-mode-map (kbd "x")))
  (should-not (lookup-key safeslop-profiles-mode-map (kbd "n")))
  (should (eq (lookup-key safeslop-profiles-mode-map (kbd "D")) #'safeslop-profiles-delete))
  (should (eq (lookup-key safeslop-profiles-mode-map (kbd "d")) #'safeslop-doctor))
  (should-not (eq (lookup-key safeslop-profiles-mode-map (kbd "S")) #'tabulated-list-sort))
  ;; inherited surface switch keys
  (should (eq (lookup-key safeslop-profiles-mode-map (kbd "P")) #'safeslop-portal)))

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

(defconst safeslop-test-profiles--bundle-envelope
  '((bundles . [((name . "claude") (description . "Claude Code")
                 (packages . ["node" "claude-code"]))
                ((name . "web") (description . "Web stack")
                 (packages . ["node" "pnpm"]))])
    (defaults . ((claude . "claude"))))
  "Synthetic bundle catalog data for profile compose tests.")

(defconst safeslop-test-profiles--package-envelope
  '((packages . [((name . "node") (kind . "binary") (version . "22"))
                 ((name . "claude-code") (kind . "npm") (version . "1")
                  (requires . ["node"]))
                 ((name . "pnpm") (kind . "npm") (version . "9")
                  (requires . ["node"]))
                 ((name . "ripgrep") (kind . "binary") (version . "14"))]))
  "Synthetic package catalog data for profile compose tests.")

(ert-deftest safeslop-test-profiles-catalog-index-default-and-requires ()
  "Catalog helpers make inherited/default/required packages selected and locked."
  (let* ((catalog (safeslop-profiles--catalog-indexes
                   safeslop-test-profiles--bundle-envelope
                   safeslop-test-profiles--package-envelope))
         (state (safeslop-profiles--compose-state
                 "review" "claude" "container" nil '("ripgrep") "deny" "." nil catalog))
         (rows (alist-get 'package-rows state)))
    (let ((node (assoc "node" rows))
          (claude (assoc "claude-code" rows))
          (direct (assoc "ripgrep" rows)))
      (should (equal (alist-get 'source (cdr node)) "default:claude"))
      (should (alist-get 'locked (cdr node)))
      (should (equal (alist-get 'source (cdr claude)) "default:claude"))
      (should (alist-get 'locked (cdr claude)))
      (should (equal (alist-get 'source (cdr direct)) "direct"))
      (should-not (alist-get 'locked (cdr direct))))))

(ert-deftest safeslop-test-profiles-bundle-and-requires-lock-package-rows ()
  "Selected bundles and recursive requirements are locked with source labels."
  (let* ((catalog (safeslop-profiles--catalog-indexes
                   '((bundles . [((name . "web") (description . "Web")
                                  (packages . ["pnpm"]))]))
                   safeslop-test-profiles--package-envelope))
         (state (safeslop-profiles--compose-state
                 "review" "fish" "container" '("web") nil "deny" "." nil catalog))
         (rows (alist-get 'package-rows state)))
    (should (equal (alist-get 'source (cdr (assoc "pnpm" rows))) "bundle:web"))
    (should (alist-get 'locked (cdr (assoc "pnpm" rows))))
    (should (equal (alist-get 'source (cdr (assoc "node" rows))) "requires:pnpm"))
    (should (alist-get 'locked (cdr (assoc "node" rows))))))

(ert-deftest safeslop-test-profiles-marker-suggestions ()
  "Local project markers map to visible bundle suggestions."
  (let ((dir (make-temp-file "safeslop-markers" t)))
    (unwind-protect
        (progn
          (dolist (file '("go.mod" "package.json" "pyproject.toml" "Cargo.toml"))
            (write-region "" nil (expand-file-name file dir)))
          (should (equal (safeslop-profiles--bundle-suggestions dir)
                         '("go" "web" "python" "rust"))))
      (delete-directory dir t))))

(ert-deftest safeslop-test-profiles-compose-keymap ()
  "Compose buffer binds checkbox/help/refresh/save/cancel keys."
  (should (eq (lookup-key safeslop-profiles-compose-mode-map (kbd "RET"))
              #'safeslop-profiles-compose-toggle))
  (should-not (eq (lookup-key safeslop-profiles-compose-mode-map (kbd "SPC"))
                  #'safeslop-profiles-compose-toggle))
  (should (eq (lookup-key safeslop-profiles-compose-mode-map (kbd "?"))
              #'safeslop-profiles-compose-help))
  (should (eq (lookup-key safeslop-profiles-compose-mode-map (kbd "g"))
              #'safeslop-profiles-compose-refresh))
  (should (eq (lookup-key safeslop-profiles-compose-mode-map (kbd "C-c C-c"))
              #'safeslop-profiles-compose-preview-save))
  (should (eq (lookup-key safeslop-profiles-compose-mode-map (kbd "q"))
              #'safeslop-profiles-compose-cancel)))

(ert-deftest safeslop-test-profiles-compose-render-help-and-locked-toggle ()
  "Compose rendering marks checked/locked rows, help uses catalog detail, and locks do not toggle."
  (let* ((catalog (safeslop-profiles--catalog-indexes
                   safeslop-test-profiles--bundle-envelope
                   safeslop-test-profiles--package-envelope))
         (state (safeslop-profiles--compose-state
                 "review" "claude" "container" nil '("ripgrep") "deny" "." nil catalog))
         help)
    (with-current-buffer (get-buffer-create "*safeslop profile compose test*")
      (unwind-protect
          (progn
            (safeslop-profiles-compose-mode)
            (setq safeslop-profiles-compose--state state)
            (safeslop-profiles-compose--render)
            (goto-char (point-min))
            (should (search-forward "[x] L bundle claude" nil t))
            (should (search-forward "default:claude" nil t))
            (goto-char (point-min))
            (should (search-forward "[x] L node" nil t))
            (should (search-forward "default:claude" nil t))
            (cl-letf (((symbol-function 'message) (lambda (fmt &rest args)
                                                    (setq help (apply #'format fmt args)))))
              (goto-char (point-min))
              (search-forward "bundle claude")
              (safeslop-profiles-compose-help)
              (should (string-match-p "Claude Code" help))
              (goto-char (point-min))
              (search-forward "node")
              (let ((before (alist-get 'package-rows safeslop-profiles-compose--state)))
                (safeslop-profiles-compose-toggle)
                (should (equal before (alist-get 'package-rows safeslop-profiles-compose--state))))
              (goto-char (point-min))
              (search-forward "bundle claude")
              (let ((before (alist-get 'bundles safeslop-profiles-compose--state)))
                (safeslop-profiles-compose-toggle)
                (should (equal before (alist-get 'bundles safeslop-profiles-compose--state))))))
        (kill-buffer (current-buffer))))))

(ert-deftest safeslop-test-profiles-compose-renders-all-catalog-packages ()
  "Package picker includes unchecked catalog packages that can become direct selections."
  (let* ((catalog (safeslop-profiles--catalog-indexes
                   safeslop-test-profiles--bundle-envelope
                   safeslop-test-profiles--package-envelope))
         (state (safeslop-profiles--compose-state
                 "review" "claude" "container" nil nil "deny" "." nil catalog)))
    (with-current-buffer (get-buffer-create "*safeslop profile packages test*")
      (unwind-protect
          (progn
            (safeslop-profiles-compose-mode)
            (setq safeslop-profiles-compose--state state)
            (safeslop-profiles-compose--render)
            (goto-char (point-min))
            (should (search-forward "[ ]   ripgrep" nil t))
            (safeslop-profiles-compose-toggle)
            (should (member "ripgrep" (alist-get 'packages safeslop-profiles-compose--state)))
            (goto-char (point-min))
            (should (search-forward "[x]   ripgrep" nil t)))
        (kill-buffer (current-buffer))))))

(ert-deftest safeslop-test-profiles-interactive-create-opens-compose-buffer ()
  "Interactive create opens the compose buffer instead of writing immediately."
  (let (called)
    (cl-letf (((symbol-function 'called-interactively-p) (lambda (&rest _) t))
              ((symbol-function 'safeslop-profiles-compose-open)
               (lambda () (setq called t))))
      (safeslop-profiles-create)
      (should called))))

(ert-deftest safeslop-test-profiles-preview-save-dry-run-before-write ()
  "Preview save runs dry-run first, shows engine safety text, then writes only on yes."
  (let* ((catalog (safeslop-profiles--catalog-indexes nil nil))
         (state (safeslop-profiles--compose-state
                 "review" "claude" "container" nil nil "deny" "." nil catalog))
         calls shown)
    (cl-letf (((symbol-function 'safeslop--call-json-async)
               (lambda (args cb)
                 (push args calls)
                 (funcall cb (safeslop-contract-parse-string
                              (concat "{\"schema_version\":1,\"ok\":true,\"data\":{"
                                      "\"risk\":{\"headline\":\"container deny is allowlisted\","
                                      "\"lines\":[\"host: unconfined only for host profiles\","
                                      "\"credential scope: value-free\","
                                      "\"mounts/file reach: workspace-only\"]},"
                                      "\"resolved\":{\"identitySet\":[\"node\"]},"
                                      "\"recipeID\":\"abc\"},\"warnings\":[],\"errors\":[]}")))))
              ((symbol-function 'yes-or-no-p) (lambda (_prompt) t))
              ((symbol-function 'safeslop-profiles-create)
               (lambda (&rest args) (push (cons 'write args) calls)))
              ((symbol-function 'safeslop-profiles--show-preview)
               (lambda (_args env) (setq shown (safeslop-profiles--preview-text (safeslop-contract-data env))))))
      (with-temp-buffer
        (safeslop-profiles-compose-mode)
        (setq safeslop-profiles-compose--state state)
        (safeslop-profiles-compose-preview-save))
      (should (member "--dry-run" (car (last calls))))
      (should (eq (caar calls) 'write))
      (should (string-match-p "container deny is allowlisted" shown))
      (should (string-match-p "workspace-only" shown)))))

(ert-deftest safeslop-test-profiles-preview-text-only-uses-engine-safety-lines ()
  "Preview text does not append Emacs-authored posture claims."
  (let* ((env (safeslop-contract-parse-string
               (concat "{\"schema_version\":1,\"ok\":true,\"data\":{"
                       "\"risk\":{\"headline\":\"engine headline\",\"lines\":[\"engine line\"]},"
                       "\"resolved\":{\"identitySet\":[\"node\"]},\"recipeID\":\"abc\"},"
                       "\"warnings\":[],\"errors\":[]}")))
         (shown (safeslop-profiles--preview-text (safeslop-contract-data env))))
    (should (string-match-p "engine headline" shown))
    (should (string-match-p "engine line" shown))
    (should-not (string-match-p "host: unconfined" shown))
    (should-not (string-match-p "container deny: allowlisted" shown))
    (should-not (string-match-p "credential scope: value-free" shown))
    (should-not (string-match-p "mounts/file reach: workspace-only" shown))))

(ert-deftest safeslop-test-profiles-preview-save-decline-prevents-write ()
  "Declining engine preview confirmation prevents the non-dry-run write call."
  (let* ((catalog (safeslop-profiles--catalog-indexes nil nil))
         (state (safeslop-profiles--compose-state
                 "review" "claude" "container" nil nil "deny" "." nil catalog))
         wrote)
    (cl-letf (((symbol-function 'safeslop--call-json-async)
               (lambda (_args cb)
                 (funcall cb (safeslop-contract-parse-string
                              "{\"schema_version\":1,\"ok\":true,\"data\":{\"risk\":{\"headline\":\"engine\",\"lines\":[\"line\"]}},\"warnings\":[],\"errors\":[]}"))))
              ((symbol-function 'yes-or-no-p) (lambda (_prompt) nil))
              ((symbol-function 'safeslop-profiles-create) (lambda (&rest _) (setq wrote t)))
              ((symbol-function 'safeslop-profiles--show-preview) (lambda (&rest _) nil)))
      (with-temp-buffer
        (safeslop-profiles-compose-mode)
        (setq safeslop-profiles-compose--state state)
        (safeslop-profiles-compose-preview-save))
      (should-not wrote))))

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
