;;; safeslop-contract-test.el --- Contract/fake CLI tests -*- lexical-binding: t; -*-

(require 'ert)
(require 'json)
(require 'subr-x)
(require 'safeslop)
(require 'safeslop-contract)
(require 'safeslop-session)

(defvar safeslop-test--argv-log nil
  "Path to the fake CLI argv log for the current test.")

(defun safeslop-test--repo-root ()
  "Return the safeslop repo root from this test file path."
  (file-truename
   (expand-file-name
    "../.."
    (file-name-directory
     (or load-file-name buffer-file-name default-directory)))))

(defun safeslop-test--fixture (name)
  "Return raw JSON for golden fixture NAME."
  (with-temp-buffer
    (insert-file-contents
     (expand-file-name (concat "internal/jsoncontract/testdata/" name)
                       (safeslop-test--repo-root)))
    (buffer-string)))

(defun safeslop-test--json-key (argv)
  "Return stable JSON-array key for ARGV."
  (json-serialize (vconcat argv)))

(defun safeslop-test--write-fake-cli (dir routes log)
  "Create fake safeslop executable in DIR with ROUTES and LOG path."
  (let* ((routes-file (expand-file-name "routes.json" dir))
         (fake (expand-file-name "safeslop" dir))
         (json-object-type 'alist)
         (json-array-type 'list))
    (with-temp-file routes-file
      (let ((route-map (make-hash-table :test 'equal)))
        (dolist (route routes)
          (puthash (car route) (cdr route) route-map))
        (insert (json-serialize route-map))))
    (with-temp-file fake
      (insert "#!/usr/bin/env python3\n"
              "import json, pathlib, sys\n"
              "routes = json.loads(pathlib.Path(" (prin1-to-string routes-file) ").read_text())\n"
              "log = pathlib.Path(" (prin1-to-string log) ")\n"
              "key = json.dumps(sys.argv[1:], separators=(',', ':'))\n"
              "with log.open('a') as f:\n"
              "    f.write(key + '\\n')\n"
              "route = routes.get(key)\n"
              "if route is None:\n"
              "    sys.stderr.write('unregistered argv: ' + key + '\\n')\n"
              "    sys.exit(97)\n"
              "sys.stdout.write(route.get('stdout', ''))\n"
              "sys.stderr.write(route.get('stderr', ''))\n"
              "sys.exit(int(route.get('exit', 0)))\n"))
    (set-file-modes fake #o755)
    fake))

(defmacro safeslop-test--with-fake-cli (routes &rest body)
  "Run BODY with `safeslop-program' set to a temp fake CLI.
ROUTES maps exact argv JSON strings to `((stdout . STRING) (stderr . STRING)
(exit . NUMBER))'.  Every argv is appended to `safeslop-test--argv-log'."
  (declare (indent 1))
  `(let* ((tmp (make-temp-file "safeslop-fake-cli" t))
          (safeslop-test--argv-log (expand-file-name "argv.log" tmp))
          (safeslop-autostart-daemon nil)
          (safeslop-program (safeslop-test--write-fake-cli tmp ,routes safeslop-test--argv-log)))
     (unwind-protect
         (progn ,@body)
       (delete-directory tmp t))))

(defun safeslop-test--argv-log-lines ()
  "Return fake CLI argv log lines."
  (when (file-exists-p safeslop-test--argv-log)
    (split-string (string-trim (with-temp-buffer
                                 (insert-file-contents safeslop-test--argv-log)
                                 (buffer-string)))
                  "\n" t)))

(ert-deftest safeslop-test-go-golden-fixtures-parse-in-emacs ()
  (let ((fixtures (directory-files
                   (expand-file-name "internal/jsoncontract/testdata" (safeslop-test--repo-root))
                   t "\\.golden\\.json\\'")))
    (should (= (length fixtures) 8))
    (dolist (fixture fixtures)
      (let ((envelope (safeslop-contract-parse-file fixture)))
        (should (= (alist-get 'schema_version envelope) safeslop-contract-schema-version))
        (should (listp (safeslop-contract-warnings envelope)))
        (should (listp (safeslop-contract-errors envelope)))))))

(ert-deftest safeslop-test-doctor-parses-ok-envelope ()
  (let* ((routes `((,(safeslop-test--json-key '("doctor" "--json")) .
                   ((stdout . ,(safeslop-test--fixture "ok-minimal.golden.json"))
                    (exit . 0))))))
    (safeslop-test--with-fake-cli routes
      (let ((envelope (safeslop-doctor)))
        (should (safeslop-contract-ok-p envelope))
        (should (equal (safeslop-test--argv-log-lines)
                       (list (safeslop-test--json-key '("doctor" "--json")))))))))

(ert-deftest safeslop-test-policy-check-surfaces-warning ()
  (let* ((policy (expand-file-name "safeslop.cue" (safeslop-test--repo-root)))
         (argv (list "validate" policy "--json"))
         (routes `((,(safeslop-test--json-key argv) .
                   ((stdout . ,(safeslop-test--fixture "ok-policy-check-with-warning.golden.json"))
                    (exit . 0))))))
    (safeslop-test--with-fake-cli routes
      (let ((envelope (safeslop-policy-check-file policy)))
        (should (safeslop-contract-ok-p envelope))
        (should (equal (alist-get 'code (car (safeslop-contract-warnings envelope)))
                       "POLICY_DENIED"))))))

(ert-deftest safeslop-test-session-new-claude-code-exact-argv ()
  (let* ((workspace (safeslop-test--repo-root))
         (argv (list "session" "create" "--agent" "claude" "--workspace" workspace "--output" "json"))
         (routes `((,(safeslop-test--json-key argv) .
                   ((stdout . ,(safeslop-test--fixture "ok-session-create.golden.json"))
                    (exit . 0))))))
    (safeslop-test--with-fake-cli routes
      (should (safeslop-contract-ok-p (safeslop-session-new "claude" workspace)))
      (should (equal (safeslop-test--argv-log-lines) (list (safeslop-test--json-key argv)))))))

(ert-deftest safeslop-test-session-new-pi-exact-argv ()
  (let* ((workspace (safeslop-test--repo-root))
         (argv (list "session" "create" "--agent" "pi" "--workspace" workspace "--output" "json"))
         (routes `((,(safeslop-test--json-key argv) .
                   ((stdout . ,(safeslop-test--fixture "ok-session-create.golden.json"))
                    (exit . 0))))))
    (safeslop-test--with-fake-cli routes
      (should (safeslop-contract-ok-p (safeslop-session-new "pi" workspace)))
      (should (equal (safeslop-test--argv-log-lines) (list (safeslop-test--json-key argv)))))))

(ert-deftest safeslop-test-unsupported-agent-error-code ()
  (let* ((workspace (safeslop-test--repo-root))
         (argv (list "session" "create" "--agent" "cursor" "--workspace" workspace "--output" "json"))
         (routes `((,(safeslop-test--json-key argv) .
                   ((stdout . ,(safeslop-test--fixture "error-agent-unsupported.golden.json"))
                    (exit . 2))))))
    (safeslop-test--with-fake-cli routes
      (let ((envelope (safeslop-session-new "cursor" workspace)))
        (should-not (safeslop-contract-ok-p envelope))
        (should (equal (safeslop-contract-first-error-code envelope) "AGENT_UNSUPPORTED"))
        (should (equal safeslop-last-error "AGENT_UNSUPPORTED"))))))

(ert-deftest safeslop-test-session-stop-revokes-credentials ()
  (let* ((argv '("session" "stop" "--session-id" "sess-1" "--revoke-credentials" "--output" "json"))
         (routes `((,(safeslop-test--json-key argv) .
                   ((stdout . ,(safeslop-test--fixture "ok-minimal.golden.json"))
                    (exit . 0))))))
    (safeslop-test--with-fake-cli routes
      (should (safeslop-contract-ok-p (safeslop-session-stop "sess-1")))
      (should (equal (safeslop-test--argv-log-lines) (list (safeslop-test--json-key argv)))))))

(ert-deftest safeslop-test-workspace-path-never-shell-expanded ()
  (let* ((tmp (make-temp-file "safeslop a;b $(touch pwn)" t))
         (workspace (expand-file-name tmp))
         (pwn (expand-file-name "pwn" (file-name-directory (directory-file-name workspace))))
         (argv (list "session" "create" "--agent" "claude" "--workspace" workspace "--output" "json"))
         (routes `((,(safeslop-test--json-key argv) .
                   ((stdout . ,(safeslop-test--fixture "ok-session-create.golden.json"))
                    (exit . 0))))))
    (unwind-protect
        (safeslop-test--with-fake-cli routes
          (should (safeslop-contract-ok-p (safeslop-session-new "claude" workspace)))
          (should-not (file-exists-p pwn))
          (should (equal (safeslop-test--argv-log-lines) (list (safeslop-test--json-key argv)))))
      (delete-directory tmp t))))

(ert-deftest safeslop-test-fallback-compilation-mode-on-pty-unavailable ()
  (let* ((argv '("session" "status" "--session-id" "sess-pty" "--output" "jsonl"))
         (routes `((,(safeslop-test--json-key argv) .
                   ((stdout . "{\"schema_version\":1,\"ok\":true}\n")
                    (exit . 0))))))
    (safeslop-test--with-fake-cli routes
      (let ((buf (safeslop-session-status-fallback "sess-pty")))
        (with-current-buffer buf
          (should (derived-mode-p 'compilation-mode))
          (let ((proc (get-buffer-process buf)))
            (while (and proc (process-live-p proc))
              (accept-process-output proc 0.1))))
        (should (equal (safeslop-test--argv-log-lines) (list (safeslop-test--json-key argv))))))))

(provide 'safeslop-contract-test)
;;; safeslop-contract-test.el ends here
