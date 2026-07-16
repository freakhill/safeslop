;;; safeslop-credentials-test.el --- Tests for safeslop-credentials.el -*- lexical-binding: t; -*-

(require 'ert)
(require 'cl-lib)
(require 'safeslop)
(require 'safeslop-surface)
(require 'safeslop-credentials)
(require 'safeslop-doom)
(require 'safeslop-contract)

(defconst safeslop-test-profile-list-json
  "{\"schema_version\":1,\"ok\":true,\"data\":{\"path\":\"/ws/safeslop.cue\",\"profiles\":{\"app\":{\"agent\":\"pi\",\"environment\":\"container\",\"network\":\"deny\"}},\"builtins\":[]},\"warnings\":[],\"errors\":[]}"
  "A project profile list used by value-free Credentials journey tests.")

(defconst safeslop-test-creds-show-empty-json
  "{\"schema_version\":1,\"ok\":true,\"data\":{\"config\":\"/ws/safeslop.cue\",\"profile\":\"app\",\"op\":{\"available\":false,\"signedIn\":false},\"credentials\":[]},\"warnings\":[],\"errors\":[]}"
  "An empty profile credential posture used by first-run journey tests.")

(defconst safeslop-test-creds-show-mixed-json
  "{\"schema_version\":1,\"ok\":true,\"data\":{\"config\":\"/ws/safeslop.cue\",\"profile\":\"app\",\"op\":{\"available\":false,\"signedIn\":false},\"credentials\":[{\"profile\":\"app\",\"kind\":\"github\",\"name\":\"acme/web\",\"scope\":\"app ro\",\"ref\":\"\",\"status\":\"ephemeral\"},{\"profile\":\"app\",\"kind\":\"github\",\"name\":\"acme/api\",\"scope\":\"app rw\",\"ref\":\"\",\"status\":\"ephemeral\"}]},\"warnings\":[],\"errors\":[]}"
  "Mixed read/write GitHub posture used to prove safe edit defaults.")

(defconst safeslop-test-mutation-ok-json
  "{\"schema_version\":1,\"ok\":true,\"data\":{\"credential_scopes\":[]},\"warnings\":[],\"errors\":[]}"
  "A value-free successful profile mutation response.")

(defconst safeslop-test-mutation-failed-json
  "{\"schema_version\":1,\"ok\":false,\"data\":{},\"warnings\":[],\"errors\":[{\"code\":\"INVALID_ARGUMENT\",\"message\":\"repo conflict\",\"details\":{},\"retryable\":false}]}"
  "A value-free failed profile mutation response.")

(defconst safeslop-test-creds-list-json
  (concat "{\"schema_version\":1,\"ok\":true,\"data\":{"
          "\"config\":\"/ws/safeslop.cue\","
          "\"op\":{\"available\":true,\"signedIn\":false},"
          "\"credentials\":["
          "{\"profile\":\"app\",\"kind\":\"secret\",\"name\":\"TOKEN\",\"scope\":\"\",\"ref\":\"env:APP_TOKEN\",\"status\":\"resolvable\"},"
          "{\"profile\":\"app\",\"kind\":\"ssh\",\"name\":\"origin\",\"scope\":\"deploy-key ro\",\"ref\":\"\",\"status\":\"ephemeral\"},"
          "{\"profile\":\"app\",\"kind\":\"aws\",\"name\":\"acme\",\"scope\":\"eu-west-1\",\"ref\":\"\",\"status\":\"ambient\"},"
          "{\"profile\":\"ci\",\"kind\":\"pnpm\",\"name\":\"npm.pkg.github.com\",\"scope\":\"@acme\",\"ref\":\"env:NPM\",\"status\":\"missing\"}"
          "]},\"warnings\":[],\"errors\":[]}")
  "A representative `creds list' envelope covering every status class.")

(ert-deftest safeslop-test-credentials-command-loads ()
  (should (fboundp 'safeslop-credentials))
  (should (fboundp 'safeslop-credentials-mode))
  (should (fboundp 'safeslop-credentials-inspect))
  (should (fboundp 'safeslop-credentials-edit))
  (should (fboundp 'safeslop-credentials-refresh)))

(ert-deftest safeslop-test-credentials-rows-from-list ()
  "`safeslop-credentials--rows' builds Profile/Kind/Name/Source/Status rows, status-faced."
  (let* ((env (safeslop-contract-parse-string safeslop-test-creds-list-json))
         (rows (safeslop-credentials--rows (safeslop-contract-data env))))
    (should (= (length rows) 4))
    (let ((secret (assoc "app/secret/TOKEN" rows)))
      (should secret)
      (should (equal (aref (cadr secret) 0) "app"))
      (should (equal (aref (cadr secret) 1) "secret"))
      (should (equal (aref (cadr secret) 2) "TOKEN"))
      (should (equal (aref (cadr secret) 3) "env:APP_TOKEN")) ; Source = ref (a ref, not a value)
      (should (equal (substring-no-properties (aref (cadr secret) 4)) "resolvable"))
      (should (eq (get-text-property 0 'face (aref (cadr secret) 4)) 'safeslop-cred-ready)))
    (let ((ssh (assoc "app/ssh/origin" rows)))
      (should (equal (aref (cadr ssh) 3) "deploy-key ro")) ; empty ref -> scope
      (should (eq (get-text-property 0 'face (aref (cadr ssh) 4)) 'safeslop-cred-ephemeral)))
    (let ((aws (assoc "app/aws/acme" rows)))
      (should (equal (aref (cadr aws) 3) "eu-west-1"))
      (should (eq (get-text-property 0 'face (aref (cadr aws) 4)) 'safeslop-cred-ambient)))
    (let ((pnpm (assoc "ci/pnpm/npm.pkg.github.com" rows)))
      (should (equal (aref (cadr pnpm) 3) "env:NPM"))
      (should (eq (get-text-property 0 'face (aref (cadr pnpm) 4)) 'safeslop-cred-missing)))))

(ert-deftest safeslop-test-credentials-never-shows-a-value ()
  "The rows carry refs, not values: a secret value never appears in any cell.
The engine already discards values; this guards the client from ever rendering
one even if a future envelope regressed to carrying it."
  (let* ((env (safeslop-contract-parse-string safeslop-test-creds-list-json))
         (rows (safeslop-credentials--rows (safeslop-contract-data env)))
         (flat (mapconcat (lambda (r)
                            (mapconcat #'substring-no-properties (append (cadr r) nil) " "))
                          rows " ")))
    ;; refs (env:APP_TOKEN) are present; a resolved value would be a distinct token.
    (should (string-match-p "env:APP_TOKEN" flat))
    (should-not (string-match-p "APP_TOKEN=" flat))))

(ert-deftest safeslop-test-credentials-status-cell-faces ()
  "Each readiness status renders as its label in the matching redundant face."
  (dolist (pair '(("resolvable" . safeslop-cred-ready)
                  ("missing" . safeslop-cred-missing)
                  ("op-signed-out" . safeslop-cred-attention)
                  ("op-unavailable" . safeslop-cred-attention)
                  ("ephemeral" . safeslop-cred-ephemeral)
                  ("ambient" . safeslop-cred-ambient)))
    (let ((cell (safeslop-credentials--status-cell (car pair))))
      (should (equal (substring-no-properties cell) (car pair)))
      (should (eq (get-text-property 0 'face cell) (cdr pair)))
      (should (get-text-property 0 'help-echo cell)))) ; honest meaning always attached
  ;; An unknown status degrades gracefully (label kept, no crash).
  (should (equal (substring-no-properties (safeslop-credentials--status-cell "weird")) "weird")))

(ert-deftest safeslop-test-credentials-source-cell ()
  "Source prefers the ref, then the scope, then a parenthetic status note."
  (should (equal (safeslop-credentials--source-cell "env:X" "" "resolvable") "env:X"))
  (should (equal (safeslop-credentials--source-cell "op://v/i/f" "pat ro" "resolvable") "op://v/i/f"))
  (should (equal (safeslop-credentials--source-cell "" "deploy-key ro" "ephemeral") "deploy-key ro"))
  (should (equal (safeslop-credentials--source-cell "" "" "ephemeral") "(ephemeral)"))
  (should (equal (safeslop-credentials--source-cell "" "" "ambient") "(ambient)"))
  (should (equal (safeslop-credentials--source-cell "" "" "missing") "")))

(ert-deftest safeslop-test-credentials-account-section-value-free ()
  "Linked account status renders non-secret ids/probe classes only."
  (with-temp-buffer
    (safeslop-credentials-mode)
    (setq safeslop-credentials--account-links
          '(((forge . "github") (host . "github.com") (owner . "acme")
             (appID . 123) (installationID . 456) (probe . "ok") (ttl . "1h-renewable"))
            ((forge . "forgejo") (host . "forgejo.example.com") (owner . "bot")
             (sshPort . 2222) (probe . "secret-unresolved") (ttl . "account-wide token"))))
    (let ((section (safeslop-credentials--account-section)))
      (should (string-match-p "github.com/acme" section))
      (should (string-match-p "app=123" section))
      (should (string-match-p "forgejo.example.com/bot" section))
      (should (string-match-p "secret-unresolved" section))
      (should-not (string-match-p "privateKeyRef\\|tokenRef\\|op://\\|TOKEN_VALUE\\|PRIVATE KEY" section)))))

(ert-deftest safeslop-test-credentials-account-status-failure-keeps-credential-rows ()
  "A failed account-link status fetch degrades the header, not the credential table."
  (let ((calls nil))
    (cl-letf (((symbol-function 'safeslop--call-json-async)
               (lambda (args callback &optional _stderr)
                 (push args calls)
                 (funcall callback
                          (cond
                           ((equal args '("creds" "status" "--output" "json"))
                            (safeslop-contract-parse-string
                             "{\"schema_version\":1,\"ok\":false,\"data\":{},\"warnings\":[],\"errors\":[{\"code\":\"IO_ERROR\",\"message\":\"status failed\",\"details\":{},\"retryable\":false}]}"))
                           ((equal args '("creds" "list" "--output" "json"))
                            (safeslop-contract-parse-string safeslop-test-creds-list-json))
                           (t (error "unexpected argv: %S" args)))))))
      (with-temp-buffer
        (safeslop-credentials-mode)
        (safeslop-credentials--render)
        (should (equal (nreverse calls)
                       '(("creds" "status" "--output" "json")
                         ("creds" "list" "--output" "json"))))
        (should (= (length tabulated-list-entries) 4))
        (should safeslop-credentials--account-status-error)
        (should (string-match-p "account links: unavailable" (buffer-string)))))))

(ert-deftest safeslop-test-credentials-link-account-github-argv ()
  "The account-link action shells out with refs/ids only, never key material."
  (let (captured refreshed shown)
    (cl-letf (((symbol-function 'completing-read) (lambda (&rest _) "github"))
              ((symbol-function 'read-string)
               (let ((answers '("github.com" "op://vault/app/private-key")))
                 (lambda (&rest _)
                   (pop answers))))
              ((symbol-function 'read-number)
               (let ((answers '(123 456)))
                 (lambda (&rest _)
                   (pop answers))))
              ((symbol-function 'safeslop-credentials--call-raw-async)
               (lambda (args callback)
                 (setq captured args)
                 (funcall callback (safeslop-contract-parse-string
                                    "{\"schema_version\":1,\"ok\":true,\"data\":{\"message\":\"linked\"},\"warnings\":[],\"errors\":[]}"))))
              ((symbol-function 'safeslop--show-envelope-buffer)
               (lambda (_name _args env) (setq shown env)))
              ((symbol-function 'safeslop-credentials-refresh)
               (lambda () (setq refreshed t))))
      (safeslop-credentials-link-account))
    (should (equal captured '("creds" "link" "github" "--host" "github.com"
                              "--app-id" "123" "--installation-id" "456"
                              "--key-ref" "op://vault/app/private-key")))
    (should (safeslop-contract-ok-p shown))
    (should refreshed)
    (should-not (member "PRIVATE KEY" captured))))

(ert-deftest safeslop-test-credentials-unlink-account-argv ()
  "The unlink action chooses host/owner from value-free status rows and refreshes."
  (let (captured refreshed)
    (with-temp-buffer
      (safeslop-credentials-mode)
      (setq safeslop-credentials--account-links
            '(((forge . "github") (host . "github.com") (owner . "acme")
               (appID . 123) (installationID . 456) (probe . "ok") (ttl . "1h-renewable"))))
      (cl-letf (((symbol-function 'completing-read) (lambda (&rest _) "github.com/acme"))
                ((symbol-function 'yes-or-no-p) (lambda (&rest _) t))
                ((symbol-function 'safeslop-credentials--call-raw-async)
                 (lambda (args callback)
                   (setq captured args)
                   (funcall callback (safeslop-contract-parse-string
                                      "{\"schema_version\":1,\"ok\":true,\"data\":{\"message\":\"unlinked\"},\"warnings\":[],\"errors\":[]}"))))
                ((symbol-function 'safeslop--show-envelope-buffer) (lambda (&rest _) nil))
                ((symbol-function 'safeslop-credentials-refresh) (lambda () (setq refreshed t))))
        (safeslop-credentials-unlink-account)))
    (should (equal captured '("creds" "unlink" "github.com/acme")))
    (should refreshed)))

(ert-deftest safeslop-test-credentials-profile-credentials-args-github-origin ()
  (should (equal (safeslop-credentials--profile-credentials-args
                  "app" "/ws/safeslop.cue" "github" t nil nil nil nil)
                 '("profile" "credentials" "set" "app" "/ws/safeslop.cue"
                   "--provider" "github" "--use-origin" "--output" "json"))))

(ert-deftest safeslop-test-credentials-profile-credentials-args-github-repos ()
  (should (equal (safeslop-credentials--profile-credentials-args
                  "app" nil "github" nil '("acme/web") '("acme/api") nil nil)
                 '("profile" "credentials" "set" "app"
                   "--provider" "github" "--repo" "acme/web"
                   "--write-repo" "acme/api" "--output" "json"))))

(ert-deftest safeslop-test-credentials-profile-credentials-args-forgejo-repos ()
  (should (equal (safeslop-credentials--profile-credentials-args
                  "app" "/ws/safeslop.cue" "forgejo" nil '("acme/web") '("acme/api")
                  "https://forgejo.example.com" "2222")
                 '("profile" "credentials" "set" "app" "/ws/safeslop.cue"
                   "--provider" "forgejo" "--url" "https://forgejo.example.com"
                   "--ssh-port" "2222" "--repo" "acme/web"
                   "--write-repo" "acme/api" "--output" "json"))))

(ert-deftest safeslop-test-credentials-repo-picker-saves-and-refreshes-in-place ()
  "The picker confirms a value-free write summary, calls the CLI, and refreshes dashboards."
  (let (captured confirmation credentials-refreshed profiles-refreshed popped)
    (let ((profiles-buf (get-buffer-create safeslop-profiles-buffer-name)))
      (unwind-protect
          (progn
            (with-current-buffer profiles-buf
              (safeslop-profiles-mode))
            (with-temp-buffer
              (safeslop-credentials-mode)
              (setq safeslop-credentials--config-path "/ws/safeslop.cue")
              (setq tabulated-list-entries
                    '(("app/github/origin" ["app" "github" "origin" "deploy-key ro" "ephemeral"])))
              (cl-letf (((symbol-function 'completing-read)
                         (let ((answers '("app" "github" "explicit repos")))
                           (lambda (&rest _) (pop answers))))
                        ((symbol-function 'read-string)
                         (let ((answers '("acme/web" "acme/api")))
                           (lambda (&rest _) (pop answers))))
                        ((symbol-function 'yes-or-no-p)
                         (lambda (prompt) (setq confirmation prompt) t))
                        ((symbol-function 'safeslop--call-json-async)
                         (lambda (args callback &optional _stderr)
                           (setq captured args)
                           (funcall callback (safeslop-contract-parse-string
                                              "{\"schema_version\":1,\"ok\":true,\"data\":{\"credential_scopes\":[]},\"warnings\":[],\"errors\":[]}"))))
                        ((symbol-function 'safeslop-credentials-refresh)
                         (lambda () (setq credentials-refreshed t)))
                        ((symbol-function 'safeslop-profiles-refresh)
                         (lambda () (setq profiles-refreshed t)))
                        ((symbol-function 'safeslop--show-envelope-buffer)
                         (lambda (&rest _) (setq popped t))))
                (safeslop-credentials-pick-repositories))))
        (kill-buffer profiles-buf)))
    (should (equal captured '("profile" "credentials" "set" "app" "/ws/safeslop.cue"
                              "--provider" "github" "--repo" "acme/web"
                              "--write-repo" "acme/api" "--output" "json")))
    (should (string-match-p "WRITE: acme/api" (substring-no-properties confirmation)))
    (should credentials-refreshed)
    (should profiles-refreshed)
    (should-not popped)))

(ert-deftest safeslop-test-credentials-repo-picker-cancel-aborts-before-cli ()
  (let ((called nil))
    (with-temp-buffer
      (safeslop-credentials-mode)
      (setq tabulated-list-entries
            '(("app/github/origin" ["app" "github" "origin" "deploy-key ro" "ephemeral"])))
      (cl-letf (((symbol-function 'completing-read)
                 (let ((answers '("app" "github" "origin inference")))
                   (lambda (&rest _) (pop answers))))
                ((symbol-function 'yes-or-no-p) (lambda (&rest _) nil))
                ((symbol-function 'safeslop--call-json-async)
                 (lambda (&rest _) (setq called t))))
        (safeslop-credentials-pick-repositories)))
    (should-not called)))

(ert-deftest safeslop-test-credentials-journey-universal-keys-dispatch ()
  "Displayed universal keys resolve to credential tasks in the raw mode map."
  (dolist (pair '(("A" . safeslop-credentials-link-account)
                  ("U" . safeslop-credentials-unlink-account)
                  ("R" . safeslop-credentials-pick-repositories)
                  ("X" . safeslop-credentials-clear-profile-forge)))
    (should (eq (lookup-key safeslop-credentials-mode-map (kbd (car pair))) (cdr pair)))))

(ert-deftest safeslop-test-credentials-journey-guidance-is-executable ()
  "Empty and header guidance advertise universal setup actions and the active refresh key."
  (with-temp-buffer
    (safeslop-credentials-mode)
    (let ((empty (substring-no-properties (safeslop-credentials--empty-state "/ws/safeslop.cue")))
          (header (substring-no-properties (safeslop-credentials--header))))
      (dolist (want '("A link" "R repos" "project profile" "g refresh"))
        (should (string-match-p (regexp-quote want) (concat empty "\n" header)))))))

(ert-deftest safeslop-test-credentials-journey-link-confirms-and-stays-in-surface ()
  "Actual A dispatch reviews identity and keeps successful setup in Credentials."
  (let (confirmation called refreshed popped)
    (with-temp-buffer
      (safeslop-credentials-mode)
      (cl-letf (((symbol-function 'completing-read) (lambda (&rest _) "github"))
                ((symbol-function 'read-string)
                 (let ((answers '("github.com" "op://vault/app/private-key")))
                   (lambda (&rest _) (pop answers))))
                ((symbol-function 'read-number)
                 (let ((answers '(123 456))) (lambda (&rest _) (pop answers))))
                ((symbol-function 'yes-or-no-p)
                 (lambda (prompt) (setq confirmation prompt) t))
                ((symbol-function 'safeslop-credentials--call-raw-async)
                 (lambda (args callback)
                   (setq called args)
                   (funcall callback (safeslop-contract-parse-string safeslop-test-mutation-ok-json))))
                ((symbol-function 'safeslop-credentials-refresh) (lambda () (setq refreshed t)))
                ((symbol-function 'safeslop--show-envelope-buffer) (lambda (&rest _) (setq popped t))))
        (call-interactively (lookup-key safeslop-credentials-mode-map (kbd "A")))))
    (should called)
    (should (string-match-p "GitHub.*github.com.*123.*456" (substring-no-properties confirmation)))
    (should-not (string-match-p "op://" (substring-no-properties confirmation)))
    (should refreshed)
    (should-not popped)))

(ert-deftest safeslop-test-credentials-journey-first-run-profile-source ()
  "A valid project profile is selectable even when `creds list' has no rows."
  (let (calls)
    (with-temp-buffer
      (safeslop-credentials-mode)
      (setq safeslop-credentials--config-path "/ws/safeslop.cue")
      (cl-letf (((symbol-function 'completing-read)
                 (let ((answers '("app" "github" "origin inference")))
                   (lambda (&rest _) (pop answers))))
                ((symbol-function 'yes-or-no-p) (lambda (&rest _) t))
                ((symbol-function 'safeslop--call-json-async)
                 (lambda (args callback &optional _stderr)
                   (push args calls)
                   (funcall callback
                            (safeslop-contract-parse-string
                             (cond
                              ((equal (car args) "profile") safeslop-test-profile-list-json)
                              ((equal (seq-take args 2) '("creds" "show")) safeslop-test-creds-show-empty-json)
                              (t safeslop-test-mutation-ok-json))))))
                ((symbol-function 'safeslop-credentials-refresh) (lambda () nil)))
        (call-interactively (lookup-key safeslop-credentials-mode-map (kbd "R")))))
    (should (equal (nreverse calls)
                   '(("profile" "list" "/ws/safeslop.cue" "--output" "json")
                     ("creds" "show" "app" "/ws/safeslop.cue" "--output" "json")
                     ("profile" "credentials" "set" "app" "/ws/safeslop.cue"
                      "--provider" "github" "--use-origin" "--output" "json"))))))

(ert-deftest safeslop-test-credentials-journey-existing-scopes-prefill-and-warn ()
  "Existing mixed scopes seed defaults and the full replacement is explicit."
  (let (calls defaults confirmation)
    (with-temp-buffer
      (safeslop-credentials-mode)
      (setq safeslop-credentials--config-path "/ws/safeslop.cue")
      (cl-letf (((symbol-function 'completing-read)
                 (let ((answers '("app" "github" "explicit repos")))
                   (lambda (&rest _) (pop answers))))
                ((symbol-function 'read-string)
                 (lambda (prompt &optional _initial _history default)
                   (push (cons prompt default) defaults)
                   (or default "")))
                ((symbol-function 'yes-or-no-p)
                 (lambda (prompt) (setq confirmation prompt) t))
                ((symbol-function 'safeslop--call-json-async)
                 (lambda (args callback &optional _stderr)
                   (push args calls)
                   (funcall callback
                            (safeslop-contract-parse-string
                             (cond
                              ((equal (car args) "profile") safeslop-test-profile-list-json)
                              ((equal (seq-take args 2) '("creds" "show")) safeslop-test-creds-show-mixed-json)
                              (t safeslop-test-mutation-ok-json))))))
                ((symbol-function 'safeslop-credentials-refresh) (lambda () nil)))
        (safeslop-credentials-pick-repositories)))
    (should (equal (cdr (assoc "Read-only repos (owner/name, comma-separated): " defaults)) "acme/web"))
    (should (equal (cdr (assoc "Write repos (owner/name, comma-separated): " defaults)) "acme/api"))
    (should (string-match-p "Existing:.*acme/web.*acme/api" (substring-no-properties confirmation)))
    (should (string-match-p "replaces all.*forge" (downcase (substring-no-properties confirmation))))
    (should (member '("profile" "credentials" "set" "app" "/ws/safeslop.cue"
                      "--provider" "github" "--repo" "acme/web"
                      "--write-repo" "acme/api" "--output" "json") calls))))

(ert-deftest safeslop-test-credentials-journey-failed-scope-retains-draft ()
  "A value-free failed scope write retains defaults for R retry."
  (with-temp-buffer
    (safeslop-credentials-mode)
    (setq safeslop-credentials--config-path "/ws/safeslop.cue")
    (cl-letf (((symbol-function 'completing-read)
               (let ((answers '("app" "github" "explicit repos")))
                 (lambda (&rest _) (pop answers))))
              ((symbol-function 'read-string)
               (let ((answers '("acme/web" "acme/api")))
                 (lambda (&rest _) (pop answers))))
              ((symbol-function 'yes-or-no-p) (lambda (&rest _) t))
              ((symbol-function 'safeslop--call-json-async)
               (lambda (args callback &optional _stderr)
                 (funcall callback
                          (safeslop-contract-parse-string
                           (cond
                            ((equal (car args) "profile") safeslop-test-profile-list-json)
                            ((equal (seq-take args 2) '("creds" "show")) safeslop-test-creds-show-empty-json)
                            (t safeslop-test-mutation-failed-json))))))
              ((symbol-function 'safeslop--show-envelope-buffer) (lambda (&rest _) nil)))
      (safeslop-credentials-pick-repositories))
    (should (equal (alist-get 'profile safeslop-credentials--repo-draft) "app"))
    (should (equal (alist-get 'read-repos safeslop-credentials--repo-draft) '("acme/web")))
    (should (equal (alist-get 'write-repos safeslop-credentials--repo-draft) '("acme/api")))))

(ert-deftest safeslop-test-credentials-journey-clear-profile-not-account ()
  "X confirms and calls profile forge clear, never account unlink."
  (let (calls confirmation)
    (with-temp-buffer
      (safeslop-credentials-mode)
      (setq safeslop-credentials--config-path "/ws/safeslop.cue")
      (cl-letf (((symbol-function 'completing-read) (lambda (&rest _) "app"))
                ((symbol-function 'yes-or-no-p) (lambda (prompt) (setq confirmation prompt) t))
                ((symbol-function 'safeslop--call-json-async)
                 (lambda (args callback &optional _stderr)
                   (push args calls)
                   (funcall callback (safeslop-contract-parse-string
                                      (if (equal (car args) "profile")
                                          (if (equal (cadr args) "list") safeslop-test-profile-list-json safeslop-test-mutation-ok-json)
                                        safeslop-test-mutation-ok-json)))))
                ((symbol-function 'safeslop-credentials-refresh) (lambda () nil)))
        (call-interactively (lookup-key safeslop-credentials-mode-map (kbd "X")))))
    (should (string-match-p "profile.*forge.*account link.*remain" (downcase (substring-no-properties confirmation))))
    (should (member '("profile" "credentials" "clear" "app" "/ws/safeslop.cue" "--output" "json") calls))
    (should-not (seq-some (lambda (args) (equal (seq-take args 2) '("creds" "unlink"))) calls))))

(ert-deftest safeslop-test-credentials-op-legend ()
  "The op legend names each 1Password state, faced, and stays value-free."
  (should (string-match-p "signed in"
                          (safeslop-credentials--op-legend '((available . t) (signedIn . t)))))
  (should (string-match-p "not signed in"
                          (safeslop-credentials--op-legend '((available . t) (signedIn . nil)))))
  (should (string-match-p "op CLI not found"
                          (safeslop-credentials--op-legend '((available . nil) (signedIn . nil)))))
  (should (string-match-p "checking" (safeslop-credentials--op-legend nil))))

(ert-deftest safeslop-test-credentials-surface-registered ()
  "The Credentials surface is a first-class tab with switch key K (Keys)."
  (let ((entry (assq 'credentials safeslop-surface--order)))
    (should entry)
    (should (equal (nth 1 entry) "Credentials"))
    (should (equal (nth 2 entry) "K"))
    (should (eq (nth 3 entry) 'safeslop-credentials)))
  (with-temp-buffer
    (safeslop-credentials-mode)
    (should (eq (safeslop-surface--current-sym) 'credentials))))

(ert-deftest safeslop-test-credentials-keymap ()
  "RET/i inspect (safe primary), e edit, g refresh; no destructive keys; switch keys inherit."
  (should (eq (lookup-key safeslop-credentials-mode-map (kbd "RET")) #'safeslop-credentials-inspect))
  (should (eq (lookup-key safeslop-credentials-mode-map (kbd "i")) #'safeslop-credentials-inspect))
  (should (eq (lookup-key safeslop-credentials-mode-map (kbd "e")) #'safeslop-credentials-edit))
  (should (eq (lookup-key safeslop-credentials-mode-map (kbd "g")) #'safeslop-credentials-refresh))
  (should (eq (lookup-key safeslop-credentials-mode-map (kbd "a")) #'safeslop-credentials-link-account))
  (should (eq (lookup-key safeslop-credentials-mode-map (kbd "u")) #'safeslop-credentials-unlink-account))
  (should (eq (lookup-key safeslop-credentials-mode-map (kbd "p")) #'safeslop-credentials-pick-repositories))
  ;; read-only surface: no launch/create/delete affordances
  (should-not (lookup-key safeslop-credentials-mode-map (kbd "r")))
  (should-not (lookup-key safeslop-credentials-mode-map (kbd "c")))
  (should-not (lookup-key safeslop-credentials-mode-map (kbd "D")))
  ;; inherited surface switch keys (parent map)
  (should (eq (lookup-key safeslop-credentials-mode-map (kbd "P")) #'safeslop-portal))
  (should (eq (lookup-key safeslop-credentials-mode-map (kbd "F")) #'safeslop-profiles))
  (should (eq (lookup-key safeslop-credentials-mode-map (kbd "K")) #'safeslop-credentials))
  (should (eq (lookup-key safeslop-credentials-mode-map (kbd "d")) #'safeslop-doctor)))

(ert-deftest safeslop-test-credentials-surface-switch-key-bound ()
  "K reaches the Credentials surface from every dashboard, and from `C-c s'."
  (should (eq (lookup-key safeslop-surface-mode-map (kbd "K")) #'safeslop-credentials))
  (should (eq (lookup-key safeslop-command-map (kbd "K")) #'safeslop-credentials)))

(ert-deftest safeslop-test-credentials-show-args-use-known-config-path ()
  "Inspect uses the listed safeslop.cue even after cwd changes."
  (with-temp-buffer
    (safeslop-credentials-mode)
    (setq safeslop-credentials--config-path "/repo/safeslop.cue")
    (should (equal (safeslop-credentials--show-args "app")
                   '("creds" "show" "app" "/repo/safeslop.cue" "--output" "json"))))
  (with-temp-buffer
    (safeslop-credentials-mode)
    (setq safeslop-credentials--config-path nil)
    (should (equal (safeslop-credentials--show-args "app")
                   '("creds" "show" "app" "--output" "json")))))

(ert-deftest safeslop-test-credentials-row-id-roundtrip ()
  (should (equal (safeslop-credentials--row-id "app" "ssh" "origin") "app/ssh/origin"))
  (should (equal (safeslop-credentials--row-profile "app/ssh/origin") "app")))

(ert-deftest safeslop-test-credentials-edit-needs-config ()
  (with-temp-buffer
    (safeslop-credentials-mode)
    (setq safeslop-credentials--config-path nil)
    (should-error (safeslop-credentials-edit) :type 'user-error)))

(ert-deftest safeslop-test-credentials-goto-credentials-field ()
  "Edit navigation lands on the profile's credentials/secrets field when present."
  (with-temp-buffer
    (insert "package safeslop\n"
            "safeslop: profiles: {\n"
            "\tapp: {\n"
            "\t\tagent: \"claude\"\n"
            "\t\tcredentials: {ssh: {}}\n"
            "\t}\n}\n")
    (should (safeslop-profiles--goto-profile-block "app"))
    (should (safeslop-credentials--goto-credentials-field))
    (should (string-match-p "credentials:" (buffer-substring (line-beginning-position) (line-end-position))))))

(ert-deftest safeslop-test-credentials-doom-evil-parity ()
  "The Doom/Evil tables carry the Credentials surface so bindings can't drift."
  ;; K reaches the surface from every safeslop buffer's Evil normal state.
  (should (equal (assoc "K" safeslop-doom--evil-shared-keys)
                 '("K" . safeslop-credentials)))
  ;; The surface has its own Evil action block mirroring the read-only keymap.
  (let ((entry (assq 'safeslop-credentials-mode safeslop-doom--evil-mode-keys)))
    (should entry)
    (should (eq (nth 1 entry) 'safeslop-credentials-mode-map))
    (should (eq (cdr (assoc "RET" (cddr entry))) 'safeslop-credentials-inspect))
    (should (eq (cdr (assoc "e" (cddr entry))) 'safeslop-credentials-edit))
    (should (eq (cdr (assoc "gr" (cddr entry))) 'safeslop-credentials-refresh))
    ;; read-only: no launch/create/delete in the Evil table either
    (should-not (assoc "r" (cddr entry)))
    (should-not (assoc "c" (cddr entry)))))

;;; safeslop-credentials-test.el ends here
