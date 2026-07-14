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

(defun safeslop-test-profiles--large-compose-catalog ()
  "Return a catalog with enough rows to exercise compose scroll preservation."
  (let* ((base (alist-get 'packages safeslop-test-profiles--package-envelope))
         (extras (cl-loop for n from 1 to 50
                          collect `((name . ,(format "pkg%02d" n))
                                    (kind . "binary")
                                    (version . "1"))))
         (packages (vconcat (append base extras))))
    (safeslop-profiles--catalog-indexes
     safeslop-test-profiles--bundle-envelope
     `((packages . ,packages)))))

(defun safeslop-test-profiles--compose-row-at (position)
  "Return compose row metadata at POSITION in the current compose buffer."
  (save-excursion
    (goto-char position)
    (safeslop-profiles-compose--row-at-point)))

(ert-deftest safeslop-test-profiles-compose-toggle-preserves-row-and-scroll ()
  "RET keeps an operator on the lower row they just changed."
  (let* ((catalog (safeslop-test-profiles--large-compose-catalog))
         (state (safeslop-profiles--compose-state
                 "review" "claude" "container" nil nil "deny" "." nil catalog))
         (buffer (generate-new-buffer " *safeslop compose scroll*")))
    (unwind-protect
        (save-window-excursion
          (switch-to-buffer buffer)
          (safeslop-profiles-compose-mode)
          (setq safeslop-profiles-compose--state state)
          (safeslop-profiles-compose--render)
          (goto-char (point-min))
          (should (search-forward "pkg50" nil t))
          (beginning-of-line)
          (let* ((window (selected-window))
                 (row (copy-tree (safeslop-profiles-compose--row-at-point)))
                 (point-before (point)))
            (set-window-start window point-before t)
            (let ((start-row (copy-tree
                              (safeslop-test-profiles--compose-row-at
                               (window-start window)))))
              (call-interactively #'safeslop-profiles-compose-toggle)
              (should (member "pkg50" (alist-get 'packages state)))
              (should (equal row (safeslop-profiles-compose--row-at-point)))
              (should (equal start-row
                             (safeslop-test-profiles--compose-row-at
                              (window-start window)))))))
      (when (buffer-live-p buffer)
        (kill-buffer buffer)))))

(ert-deftest safeslop-test-profiles-compose-toggle-preserves-every-showing-window ()
  "RET restores distinct row and scroll context in every compose window."
  (let* ((catalog (safeslop-test-profiles--large-compose-catalog))
         (state (safeslop-profiles--compose-state
                 "review" "claude" "container" nil nil "deny" "." nil catalog))
         (buffer (generate-new-buffer " *safeslop compose windows*")))
    (unwind-protect
        (save-window-excursion
          (switch-to-buffer buffer)
          (safeslop-profiles-compose-mode)
          (setq safeslop-profiles-compose--state state)
          (safeslop-profiles-compose--render)
          (let ((first (selected-window))
                (second (split-window-right)))
            (set-window-buffer second buffer)
            (cl-labels ((place-at
                         (window name)
                         (with-selected-window window
                           (goto-char (point-min))
                           (should (search-forward name nil t))
                           (beginning-of-line)
                           (set-window-start window (point) t)
                           (list (copy-tree (safeslop-profiles-compose--row-at-point))
                                 (copy-tree
                                  (safeslop-test-profiles--compose-row-at
                                   (window-start window)))))))
              (let ((first-rows (place-at first "pkg50"))
                    (second-rows (place-at second "pkg40")))
                (with-selected-window first
                  (goto-char (point-min))
                  (should (search-forward "pkg50" nil t))
                  (beginning-of-line)
                  (call-interactively #'safeslop-profiles-compose-toggle))
                (dolist (view `((,first ,first-rows) (,second ,second-rows)))
                  (let ((window (nth 0 view))
                        (expected (nth 1 view)))
                    (with-selected-window window
                      (should (equal (nth 0 expected)
                                     (safeslop-profiles-compose--row-at-point)))
                      (should (equal (nth 1 expected)
                                     (safeslop-test-profiles--compose-row-at
                                      (window-start window))))))))))
      (when (buffer-live-p buffer)
        (kill-buffer buffer))))))

(ert-deftest safeslop-test-profiles-compose-refresh-preserves-row-and-scroll ()
  "Refresh retains selected rows and the lower-row operator context."
  (let* ((catalog (safeslop-test-profiles--large-compose-catalog))
         (state (safeslop-profiles--compose-state
                 "review" "claude" "container" nil '("pkg50") "deny" "." nil catalog))
         (buffer (generate-new-buffer " *safeslop compose refresh*")))
    (unwind-protect
        (save-window-excursion
          (switch-to-buffer buffer)
          (safeslop-profiles-compose-mode)
          (setq safeslop-profiles-compose--state state)
          (safeslop-profiles-compose--render)
          (goto-char (point-min))
          (should (search-forward "pkg50" nil t))
          (beginning-of-line)
          (let* ((window (selected-window))
                 (row (copy-tree (safeslop-profiles-compose--row-at-point)))
                 (point-before (point)))
            (set-window-start window point-before t)
            (let ((start-row (copy-tree
                              (safeslop-test-profiles--compose-row-at
                               (window-start window)))))
              (cl-letf (((symbol-function 'safeslop-profiles--fetch-compose-catalog)
                         (lambda () catalog)))
                (call-interactively #'safeslop-profiles-compose-refresh))
              (should (equal '("pkg50") (alist-get 'packages state)))
              (should (equal row (safeslop-profiles-compose--row-at-point)))
              (should (equal start-row
                             (safeslop-test-profiles--compose-row-at
                              (window-start window)))))))
      (when (buffer-live-p buffer)
        (kill-buffer buffer)))))

(ert-deftest safeslop-test-profiles-compose-locked-row-explains-without-moving ()
  "Locked inherited rows retain context and explain why RET is unavailable."
  (let* ((catalog (safeslop-test-profiles--large-compose-catalog))
         (state (safeslop-profiles--compose-state
                 "review" "claude" "container" nil nil "deny" "." nil catalog))
         (buffer (generate-new-buffer " *safeslop compose locked*"))
         feedback)
    (unwind-protect
        (save-window-excursion
          (switch-to-buffer buffer)
          (safeslop-profiles-compose-mode)
          (setq safeslop-profiles-compose--state state)
          (safeslop-profiles-compose--render)
          (goto-char (point-min))
          (should (search-forward "bundle claude" nil t))
          (beginning-of-line)
          (let* ((window (selected-window))
                 (row (copy-tree (safeslop-profiles-compose--row-at-point)))
                 (point-before (point))
                 (state-before (copy-tree state))
                 (tick-before (buffer-chars-modified-tick)))
            (set-window-start window point-before t)
            (let ((start-row (copy-tree
                              (safeslop-test-profiles--compose-row-at
                               (window-start window)))))
              (cl-letf (((symbol-function 'message)
                         (lambda (format-string &rest args)
                           (setq feedback (apply #'format format-string args)))))
                (call-interactively #'safeslop-profiles-compose-toggle))
              (should (equal state-before state))
              (should (= tick-before (buffer-chars-modified-tick)))
              (should (equal row (safeslop-profiles-compose--row-at-point)))
              (should (equal start-row
                             (safeslop-test-profiles--compose-row-at
                              (window-start window))))
              (should (and feedback
                           (string-match-p "locked.*default:claude" feedback))))))
      (when (buffer-live-p buffer)
        (kill-buffer buffer)))))

(ert-deftest safeslop-test-profiles-compose-default-bundle-opt-out-is-explicit ()
  "A distinct control maps automatic default inclusion to bare-agent argv."
  (let* ((catalog (safeslop-profiles--catalog-indexes
                   safeslop-test-profiles--bundle-envelope
                   safeslop-test-profiles--package-envelope))
         (state (safeslop-profiles--compose-state
                 "review" "claude" "container" nil nil "deny" "." nil catalog)))
    (with-temp-buffer
      (safeslop-profiles-compose-mode)
      (setq safeslop-profiles-compose--state state)
      (safeslop-profiles-compose--render)
      (goto-char (point-min))
      (should (search-forward "Automatic agent bundle: [x] claude (enabled)" nil t))
      (should-not (member "--no-default-bundle" (safeslop-profiles--compose-args state)))
      (let ((row (copy-tree (safeslop-profiles-compose--row-at-point))))
        (should (eq (alist-get 'type row) 'default-bundle))
        (should (equal "claude" (alist-get 'name row)))
        (call-interactively #'safeslop-profiles-compose-toggle)
        (should (equal row (safeslop-profiles-compose--row-at-point))))
      (should (alist-get 'no-default-bundle state))
      (let ((claude (assoc "claude" (safeslop-profiles--bundle-rows
                                     "claude" nil t catalog)))
            (node (assoc "node" (alist-get 'package-rows state))))
        (should-not (alist-get 'checked (cdr claude)))
        (should-not (alist-get 'locked (cdr claude)))
        (should-not (alist-get 'checked (cdr node)))
        (should-not (alist-get 'locked (cdr node))))
      (should (member "--no-default-bundle" (safeslop-profiles--compose-args state)))
      (should (string-match-p "may not launch" (buffer-string)))
      (goto-char (point-min))
      (should (search-forward "Automatic agent bundle: [ ] claude (disabled)" nil t))
      (call-interactively #'safeslop-profiles-compose-toggle)
      (should-not (alist-get 'no-default-bundle state))
      (should-not (member "--no-default-bundle" (safeslop-profiles--compose-args state)))
      (let ((claude (assoc "claude" (safeslop-profiles--bundle-rows
                                     "claude" nil nil catalog))))
        (should (alist-get 'checked (cdr claude)))
        (should (alist-get 'locked (cdr claude)))))))

(ert-deftest safeslop-test-profiles-compose-omits-default-control-without-default ()
  "Agents without a catalog default do not get a bare-agent control."
  (let* ((catalog (safeslop-profiles--catalog-indexes
                   safeslop-test-profiles--bundle-envelope
                   safeslop-test-profiles--package-envelope))
         (state (safeslop-profiles--compose-state
                 "review" "fish" "container" nil nil "deny" "." nil catalog)))
    (with-temp-buffer
      (safeslop-profiles-compose-mode)
      (setq safeslop-profiles-compose--state state)
      (safeslop-profiles-compose--render)
      (should-not (string-match-p "Automatic agent bundle:" (buffer-string)))
      (should-not (member "--no-default-bundle" (safeslop-profiles--compose-args state))))))

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

(defun safeslop-test-profiles--evaluation-remediation (kind action-id summary)
  "Return representative typed remediation metadata for evaluation tests."
  `((kind . ,kind)
    (action_id . ,action-id)
    (summary . ,summary)
    (docs_ref . "specs/0101-profile-safety-evaluation.md#test")))

(defun safeslop-test-profiles--evaluation-finding
    (rule-id axis outcome severity title consequence &optional remediation scope-ids)
  "Return a representative structured profile evaluation finding."
  `((rule_id . ,rule-id)
    (axis . ,axis)
    (outcome . ,outcome)
    (severity . ,severity)
    (title . ,title)
    (consequence . ,consequence)
    (scope_ids . ,(or scope-ids nil))
    (remediation . ,(or remediation :json-null))))

(defun safeslop-test-profiles--evaluation-data ()
  "Return complete v1 profile DATA with legacy fields that must not win."
  (let* ((duplicate (safeslop-test-profiles--evaluation-remediation
                     "policy_change" "bound-shared-authority" "Engine guidance A"))
         (authority-findings
          (list
           (safeslop-test-profiles--evaluation-finding
            "authority.network.test" "network" "concern" "high"
            "Network title from engine" "Network consequence from engine" duplicate)
           (safeslop-test-profiles--evaluation-finding
            "authority.files.test" "files" "bounded" "info"
            "Files title from engine" "Files consequence from engine")
           (safeslop-test-profiles--evaluation-finding
            "authority.projection.test" "projection" "not_applicable" "info"
            "Projection title from engine" "Projection consequence from engine")
           (safeslop-test-profiles--evaluation-finding
            "authority.secrets.test" "secrets" "bounded" "info"
            "Secrets title from engine" "Secrets consequence from engine")
           (safeslop-test-profiles--evaluation-finding
            "authority.credentials.test" "credentials" "unknown" "high"
            "Credentials title from engine" "Credentials consequence from engine"
            duplicate '("credential.github.001"))))
         (trust-findings
          (list
           (safeslop-test-profiles--evaluation-finding
            "trust.project.changed" "trust" "fail" "high"
            "Trust title from engine" "Trust consequence from engine"
            (safeslop-test-profiles--evaluation-remediation
             "review_and_trust" "review-and-trust-policy" "Review exact bytes"))))
         (readiness-findings
          (list
           (safeslop-test-profiles--evaluation-finding
            "readiness.workspace" "readiness" "pass" "info"
            "Workspace title from engine" "Workspace consequence from engine")
           (safeslop-test-profiles--evaluation-finding
            "readiness.container-runtime" "readiness" "fail" "high"
            "Runtime title from engine" "Runtime consequence from engine"
            (safeslop-test-profiles--evaluation-remediation
             "install_helper" "install-container-runtime" "Install a reviewed runtime"))
           (safeslop-test-profiles--evaluation-finding
            "readiness.retry" "readiness" "unknown" "high"
            "Retry title from engine" "Retry consequence from engine"
            (safeslop-test-profiles--evaluation-remediation
             "retry_check" "retry-readiness-check" "Retry the local check"))))
         (scope '((scope_id . "credential.github.001")
                  (provider . "github")
                  (target . "acme/widgets")
                  (access . "read_write")
                  (lifetime . "short_lived")
                  (basis . "declared"))))
    (list
     '(name . "review")
     '(profile . ((agent . "claude") (environment . "container") (network . "deny")))
     (cons 'evaluation
           (list
            (cons 'schema_version 1)
            (cons 'authority
                  (list (cons 'findings authority-findings)
                        (cons 'credential_scopes (list scope))))
            (cons 'trust
                  (list '(state . "changed")
                        '(basis . "project_exact_bytes")
                        '(checked_at . "2026-07-14T12:34:56Z")
                        (cons 'findings trust-findings)))
            (cons 'readiness
                  (list '(state . "blocked")
                        '(checked_at . "2026-07-14T12:34:56Z")
                        (cons 'findings readiness-findings)))))
     '(risk . ((headline . "LEGACY HEADLINE MUST NOT WIN")
               (lines . ("legacy risk line"))
               (level . "high")))
     '(risk_axes . (((name . "network") (value . "legacy network")
                     (restricted . :json-false) (severity . "high"))))
     '(resolved . ((identitySet . ("node"))))
     '(recipeID . "abc"))))

(ert-deftest safeslop-test-profiles-preview-renders-evaluation-before-legacy-risk ()
  "Compose preview prefers the structured three-question evaluation."
  (let ((text (safeslop-profiles--preview-text
               (safeslop-test-profiles--evaluation-data))))
    (should (string-match-p "Authority — what it can reach" text))
    (should-not (string-match-p "LEGACY HEADLINE MUST NOT WIN" text))))

(defun safeslop-test-profiles--evaluation-button-at-action (action-id)
  "Return the current buffer's text button for ACTION-ID, or nil."
  (let ((position (point-min))
        found)
    (while (and (< position (point-max)) (not found))
      (if-let* ((button (button-at position)))
          (progn
            (when (equal (plist-get (button-get button 'button-data) :action-id)
                         action-id)
              (setq found button))
            (setq position (button-end button)))
        (setq position (1+ position))))
    found))

(defun safeslop-test-profiles--evaluation-buttons (text)
  "Return each button's typed metadata from propertized TEXT, in display order."
  (with-temp-buffer
    (insert text)
    (let ((position (point-min))
          metadata)
      (while (< position (point-max))
        (if-let* ((button (button-at position)))
            (progn
              (push (button-get button 'button-data) metadata)
              (setq position (button-end button)))
          (setq position (1+ position))))
      (nreverse metadata))))

(defun safeslop-test-profiles--evaluation-face-at (text label)
  "Return TEXT's face property at the start of LABEL."
  (with-temp-buffer
    (insert text)
    (goto-char (point-min))
    (should (search-forward label nil t))
    (get-text-property (- (point) (length label)) 'face)))

(ert-deftest safeslop-test-profiles-evaluation-v1-validates-and-renders-three-questions ()
  "V1 rendering keeps engine order and prints outcomes, timestamp, scope, and caveat."
  (let* ((data (safeslop-test-profiles--evaluation-data))
         (evaluation (alist-get 'evaluation data))
         (text (safeslop-profiles--evaluation-text data)))
    (should-not (safeslop-profiles--evaluation-validation-error evaluation))
    (let ((authority (string-match "Authority — what it can reach" text))
          (trust (string-match "Trust — is this exact policy approved?" text))
          (readiness (string-match "Readiness — can this host launch it now?" text)))
      (should (< authority trust))
      (should (< trust readiness)))
    (dolist (word '("[CONCERN]" "[BOUNDED]" "[N/A]" "[PASS]" "[FAIL]" "[UNKNOWN]"))
      (should (string-match-p (regexp-quote word) text)))
    (should (eq (safeslop-test-profiles--evaluation-face-at text "[PASS]")
                'safeslop-profile-evaluation-pass))
    (should (eq (safeslop-test-profiles--evaluation-face-at text "[UNKNOWN]")
                'safeslop-profile-evaluation-unknown))
    (should (eq (safeslop-test-profiles--evaluation-face-at text "[N/A]")
                'safeslop-profile-evaluation-not-applicable))
    (should (string-match-p "State: CHANGED" text))
    (should (string-match-p "State: BLOCKED" text))
    (should (string-match-p "Checked at: 2026-07-14T12:34:56Z" text))
    (should (string-match-p "point-in-time local snapshot" text))
    (should (string-match-p "remote authentication and authorization were not checked" text))
    (should (string-match-p "github · acme/widgets · READ WRITE · SHORT LIVED · DECLARED" text))
    ;; Array order is engine-owned: the client neither sorts titles nor parses
    ;; their prose to derive a different order.
    (should (< (string-match "Network title from engine" text)
               (string-match "Files title from engine" text)))
    (should (< (string-match "Runtime title from engine" text)
               (string-match "Retry title from engine" text)))
    (should-not (string-match-p "LEGACY HEADLINE MUST NOT WIN" text))))

(ert-deftest safeslop-test-profiles-evaluation-remediation-is-typed-and-deduped ()
  "Only the first action_id is a button; prose never becomes dispatch metadata."
  (let* ((data (safeslop-test-profiles--evaluation-data))
         (text (safeslop-profiles--evaluation-text data))
         (buttons (safeslop-test-profiles--evaluation-buttons text))
         (shared (cl-remove-if-not
                  (lambda (metadata)
                    (equal (plist-get metadata :action-id) "bound-shared-authority"))
                  buttons)))
    (should (= (length shared) 1))
    ;; Both findings remain even though their action button is collapsed.
    (should (string-match-p "Network consequence from engine" text))
    (should (string-match-p "Credentials consequence from engine" text))
    (let ((metadata (car shared)))
      (should (equal metadata
                     '(:kind "policy_change"
                       :action-id "bound-shared-authority"
                       :docs-ref "specs/0101-profile-safety-evaluation.md#test")))
      (should-not (plist-member metadata :summary))
      (let (dispatched)
        (with-temp-buffer
          (insert text)
          (let ((button (safeslop-test-profiles--evaluation-button-at-action
                         "bound-shared-authority")))
            (cl-letf (((symbol-function 'safeslop-profiles--dispatch-remediation)
                       (lambda (kind action-id docs-ref)
                         (setq dispatched (list kind action-id docs-ref)))))
              (button-activate button))))
        (should (equal dispatched
                       '("policy_change" "bound-shared-authority"
                         "specs/0101-profile-safety-evaluation.md#test")))))))

(ert-deftest safeslop-test-profiles-evaluation-prose-does-not-change-action-semantics ()
  "Changing engine prose leaves typed remediation dispatch unchanged."
  (let* ((data (safeslop-test-profiles--evaluation-data))
         (evaluation (alist-get 'evaluation data))
         (finding (car (alist-get 'findings (alist-get 'authority evaluation))))
         (remediation (alist-get 'remediation finding)))
    (setcdr (assq 'title finding) "Completely rewritten title")
    (setcdr (assq 'consequence finding) "rm -rf is displayed only as inert prose")
    (setcdr (assq 'summary remediation) "$(touch /tmp/never-executed)")
    (let ((text (safeslop-profiles--evaluation-text data))
          dispatched)
      (with-temp-buffer
        (insert text)
        (let ((button (safeslop-test-profiles--evaluation-button-at-action
                       "bound-shared-authority")))
          (cl-letf (((symbol-function 'safeslop-profiles--dispatch-remediation)
                     (lambda (&rest metadata) (setq dispatched metadata))))
            (button-activate button))))
      (should (equal dispatched
                     '("policy_change" "bound-shared-authority"
                       "specs/0101-profile-safety-evaluation.md#test")))
      (should-not (file-exists-p "/tmp/never-executed")))))

(ert-deftest safeslop-test-profiles-evaluation-unsupported-and-malformed-are-loud-unknown ()
  "Present but unsupported/malformed evaluation never falls back to legacy green."
  (dolist (mutator
           (list
            (lambda (evaluation)
              (setcdr (assq 'schema_version evaluation) 2))
            (lambda (evaluation)
              (let* ((authority (alist-get 'authority evaluation))
                     (finding (car (alist-get 'findings authority))))
                (setcdr (assq 'outcome finding) "future_green")))))
    (let* ((data (safeslop-test-profiles--evaluation-data))
           (evaluation (alist-get 'evaluation data)))
      (funcall mutator evaluation)
      (let ((text (safeslop-profiles--evaluation-text data)))
        (should (string-match-p "UNKNOWN — update required" text))
        (should (eq (safeslop-test-profiles--evaluation-face-at text "UNKNOWN")
                    'safeslop-profile-evaluation-unknown))
        (should (string-match-p "No legacy risk fallback was used" text))
        (should-not (string-match-p "LEGACY HEADLINE MUST NOT WIN" text))))))

(ert-deftest safeslop-test-profiles-evaluation-absent-uses-labeled-legacy-fallback ()
  "An absent evaluation explicitly says trust/readiness are unavailable."
  (let* ((data (assq-delete-all 'evaluation
                                (safeslop-test-profiles--evaluation-data)))
         (text (safeslop-profiles--evaluation-text data)))
    (should (string-match-p
             "Legacy safety summary — trust and readiness unavailable" text))
    (should (string-match-p "LEGACY HEADLINE MUST NOT WIN" text))
    (should (string-match-p "legacy risk line" text))
    (should (string-match-p "network: legacy network" text))
    (should-not (string-match-p "Authority — what it can reach" text))))

(ert-deftest safeslop-test-profiles-inspect-includes-evaluation-without-legacy-level ()
  "Profile inspect appends structured evaluation and does not promote risk.level."
  (let ((text (safeslop-profiles--inspect-format
               (safeslop-test-profiles--evaluation-data))))
    (should (string-match-p "Authority — what it can reach" text))
    (should (string-match-p "Readiness — can this host launch it now?" text))
    (should-not (string-match-p "LEGACY HEADLINE MUST NOT WIN" text))
    (should-not (string-match-p "Legacy level" text))))

(ert-deftest safeslop-test-profiles-launch-reviews-exact-evaluation-before-confirm ()
  "Launch fetches profile show, displays its evaluation, then confirms and uses session gates."
  (let* ((data (safeslop-test-profiles--evaluation-data))
         (envelope `((schema_version . 1) (ok . t) (data . ,data)
                     (warnings . nil) (errors . nil)))
         events fetched-args reviewed-evaluation launched launch-directory)
    (cl-letf (((symbol-function 'tabulated-list-get-id) (lambda () "review"))
              ((symbol-function 'safeslop--call-json-async)
               (lambda (args callback)
                 (setq fetched-args args)
                 (push 'fetch events)
                 (let ((default-directory "/other/"))
                   (funcall callback envelope))))
              ((symbol-function 'safeslop-profiles--show-launch-review)
               (lambda (_name _args shown-data)
                 (setq reviewed-evaluation (alist-get 'evaluation shown-data))
                 (push 'review events)))
              ((symbol-function 'yes-or-no-p)
               (lambda (prompt)
                 (should (string-match-p "after reviewing the engine evaluation" prompt))
                 (push 'confirm events)
                 t))
              ((symbol-function 'safeslop-session-new-from-profile)
               (lambda (name)
                 (setq launched name
                       launch-directory default-directory)
                 (push 'launch events))))
      (with-temp-buffer
        (safeslop-profiles-mode)
        (setq default-directory "/repo/"
              safeslop-profiles--config-path "/repo/safeslop.cue")
        (safeslop-profiles-launch))
      (should (equal fetched-args
                     '("profile" "show" "review" "/repo/safeslop.cue" "--output" "json")))
      (should (eq reviewed-evaluation (alist-get 'evaluation data)))
      (should (equal (nreverse events) '(fetch review confirm launch)))
      (should (equal launched "review"))
      (should (equal launch-directory "/repo/")))))

(ert-deftest safeslop-test-profiles-launch-declined-evaluation-review-does-not-create-session ()
  "Declining the post-evaluation confirmation leaves CLI session creation untouched."
  (let* ((data (safeslop-test-profiles--evaluation-data))
         (envelope `((schema_version . 1) (ok . t) (data . ,data)
                     (warnings . nil) (errors . nil)))
         launched)
    (cl-letf (((symbol-function 'tabulated-list-get-id) (lambda () "review"))
              ((symbol-function 'safeslop--call-json-async)
               (lambda (_args callback) (funcall callback envelope)))
              ((symbol-function 'safeslop-profiles--show-launch-review)
               (lambda (&rest _) nil))
              ((symbol-function 'yes-or-no-p) (lambda (_prompt) nil))
              ((symbol-function 'safeslop-session-new-from-profile)
               (lambda (&rest _) (setq launched t))))
      (with-temp-buffer
        (safeslop-profiles-mode)
        (safeslop-profiles-launch))
      (should-not launched))))

;;; safeslop-profiles-test.el ends here
