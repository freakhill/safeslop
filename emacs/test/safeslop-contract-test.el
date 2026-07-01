;;; safeslop-contract-test.el --- Contract/fake CLI tests -*- lexical-binding: t; -*-

(require 'ert)
(require 'json)
(require 'subr-x)
(require 'safeslop)
(require 'safeslop-contract)
(require 'safeslop-session)

(defvar safeslop-test--argv-log nil
  "Path to the fake CLI argv log for the current test.")

(defconst safeslop-test--this-file
  (or load-file-name buffer-file-name)
  "Absolute path of this test file, captured at load time.
At test-run time under `emacs --batch ... -f ert-run-tests-batch-and-exit'
both `load-file-name' and `buffer-file-name' are nil, so the repo root must
be anchored here while the file is still loading, not recomputed later.")

(defun safeslop-test--repo-root ()
  "Return the safeslop repo root.
Locate it by walking up from this test file to the directory that holds
`go.mod', so fixtures resolve identically whatever the process working
directory is (repo root, a git worktree, or anywhere else)."
  (let ((from (file-name-directory
               (or safeslop-test--this-file default-directory))))
    (file-truename
     (or (locate-dominating-file from "go.mod")
         (error "safeslop-test--repo-root: no go.mod above %s" from)))))

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
              "import json, pathlib, sys, time\n"
              "routes = json.loads(pathlib.Path(" (prin1-to-string routes-file) ").read_text())\n"
              "log = pathlib.Path(" (prin1-to-string log) ")\n"
              "key = json.dumps(sys.argv[1:], separators=(',', ':'))\n"
              "with log.open('a') as f:\n"
              "    f.write(key + '\\n')\n"
              "route = routes.get(key)\n"
              "if route is None:\n"
              "    sys.stderr.write('unregistered argv: ' + key + '\\n')\n"
              "    sys.exit(97)\n"
              "sys.stderr.write(route.get('stderr', ''))\n"
              "sys.stderr.flush()\n"
              "time.sleep(float(route.get('sleep', 0)))\n"
              "sys.stdout.write(route.get('stdout', ''))\n"
              "sys.stdout.flush()\n"
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

(defun safeslop-test--await (thunk &optional timeout)
  "Call THUNK with a capturing callback, pump the event loop until it fires, and
return the contract envelope it received.  THUNK is a function of one argument (a
callback); it must start an async safeslop command passing that callback, e.g.
\(safeslop-test--await (lambda (cb) (safeslop-doctor cb))).  Because the async
command runs the fake CLI in a real subprocess and the callback fires only in its
sentinel, awaiting also guarantees the argv log has been written before the test
inspects it.  Signals after TIMEOUT seconds (default 10) so a wedged call fails
the test instead of hanging it."
  (let ((done nil) (result nil))
    (funcall thunk (lambda (env) (setq result env done t)))
    (with-timeout ((or timeout 10) (error "safeslop-test--await: timed out"))
      (while (not done)
        (accept-process-output nil 0.05)))
    result))

(ert-deftest safeslop-test-go-golden-fixtures-parse-in-emacs ()
  (let ((fixtures (directory-files
                   (expand-file-name "internal/jsoncontract/testdata" (safeslop-test--repo-root))
                   t "\\.golden\\.json\\'")))
    (should (= (length fixtures) 9))
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
      (let ((envelope (safeslop-test--await (lambda (cb) (safeslop-doctor cb)))))
        (should (safeslop-contract-ok-p envelope))
        (should (equal (safeslop-test--argv-log-lines)
                       (list (safeslop-test--json-key '("doctor" "--json")))))))))

(ert-deftest safeslop-test-call-json-async-delivers-envelope ()
  "`safeslop--call-json-async' runs the CLI off the main thread and delivers a
parsed envelope to its callback (the non-blocking substitute for `call-json')."
  (let* ((argv '("doctor" "--json"))
         (routes `((,(safeslop-test--json-key argv) .
                   ((stdout . ,(safeslop-test--fixture "ok-minimal.golden.json"))
                    (exit . 0))))))
    (safeslop-test--with-fake-cli routes
      (let ((envelope (safeslop-test--await
                       (lambda (cb) (safeslop--call-json-async argv cb)))))
        (should (safeslop-contract-ok-p envelope))
        (should (equal (safeslop-test--argv-log-lines)
                       (list (safeslop-test--json-key argv))))))))

(ert-deftest safeslop-test-call-json-async-degrades-on-non-json ()
  "Async path degrades like the sync one: non-JSON stdout yields a CLIENT_NON_JSON
envelope via the callback, never a crash."
  (let* ((argv '("session" "list" "--output" "json"))
         (routes `((,(safeslop-test--json-key argv) .
                   ((stdout . "safeslop: unknown command") (exit . 1))))))
    (safeslop-test--with-fake-cli routes
      (let ((envelope (safeslop-test--await
                       (lambda (cb) (safeslop--call-json-async argv cb)))))
        (should-not (safeslop-contract-ok-p envelope))
        (should (equal (safeslop-contract-first-error-code envelope) "CLIENT_NON_JSON"))))))

(ert-deftest safeslop-test-policy-check-surfaces-warning ()
  (let* ((policy (expand-file-name "safeslop.cue" (safeslop-test--repo-root)))
         (argv (list "validate" policy "--json"))
         (routes `((,(safeslop-test--json-key argv) .
                   ((stdout . ,(safeslop-test--fixture "ok-policy-check-with-warning.golden.json"))
                    (exit . 0))))))
    (safeslop-test--with-fake-cli routes
      (let ((envelope (safeslop-test--await (lambda (cb) (safeslop-policy-check-file policy cb)))))
        (should (safeslop-contract-ok-p envelope))
        (should (equal (alist-get 'code (car (safeslop-contract-warnings envelope)))
                       "POLICY_DENIED"))))))

(ert-deftest safeslop-test-session-new-claude-code-exact-argv ()
  (let* ((workspace (safeslop-test--repo-root))
         (argv (list "session" "create" "--agent" "claude" "--workspace" workspace
                     "--environment" "container" "--network" "deny" "--output" "json"))
         (routes `((,(safeslop-test--json-key argv) .
                   ((stdout . ,(safeslop-test--fixture "ok-session-create.golden.json"))
                    (exit . 0))))))
    (safeslop-test--with-fake-cli routes
      (should (safeslop-contract-ok-p (safeslop-test--await (lambda (cb) (safeslop-session-new "claude" workspace cb)))))
      (should (equal (safeslop-test--argv-log-lines) (list (safeslop-test--json-key argv)))))))

(ert-deftest safeslop-test-session-new-claude-code-alias-exact-argv ()
  (let* ((workspace (safeslop-test--repo-root))
         (argv (list "session" "create" "--agent" "claude-code" "--workspace" workspace
                     "--environment" "container" "--network" "deny" "--output" "json"))
         (routes `((,(safeslop-test--json-key argv) .
                   ((stdout . ,(safeslop-test--fixture "ok-session-create.golden.json"))
                    (exit . 0))))))
    (safeslop-test--with-fake-cli routes
      (should (safeslop-contract-ok-p (safeslop-test--await (lambda (cb) (safeslop-session-new "claude-code" workspace cb)))))
      (should (equal (safeslop-test--argv-log-lines) (list (safeslop-test--json-key argv)))))))

(ert-deftest safeslop-test-session-new-pi-exact-argv ()
  (let* ((workspace (safeslop-test--repo-root))
         (argv (list "session" "create" "--agent" "pi" "--workspace" workspace
                     "--environment" "container" "--network" "deny" "--output" "json"))
         (routes `((,(safeslop-test--json-key argv) .
                   ((stdout . ,(safeslop-test--fixture "ok-session-create.golden.json"))
                    (exit . 0))))))
    (safeslop-test--with-fake-cli routes
      (should (safeslop-contract-ok-p (safeslop-test--await (lambda (cb) (safeslop-session-new "pi" workspace cb)))))
      (should (equal (safeslop-test--argv-log-lines) (list (safeslop-test--json-key argv)))))))

(ert-deftest safeslop-test-session-new-environment-network-exact-argv ()
  "ENVIRONMENT/NETWORK thread into the create argv as overrides (specs/0074, #4)."
  (let* ((workspace (safeslop-test--repo-root))
         (argv (list "session" "create" "--agent" "claude" "--workspace" workspace
                     "--environment" "container" "--network" "allow" "--output" "json"))
         (routes `((,(safeslop-test--json-key argv) .
                   ((stdout . ,(safeslop-test--fixture "ok-session-create.golden.json"))
                    (exit . 0))))))
    (safeslop-test--with-fake-cli routes
      (should (safeslop-contract-ok-p
               (safeslop-test--await
                (lambda (cb) (safeslop-session-new "claude" workspace cb "container" "allow")))))
      (should (equal (safeslop-test--argv-log-lines) (list (safeslop-test--json-key argv)))))))

(ert-deftest safeslop-test-session-new-omits-empty-environment-network ()
  "An empty ENVIRONMENT/NETWORK appends no flag, leaving the engine default (#4)."
  (let* ((workspace (safeslop-test--repo-root))
         (argv (list "session" "create" "--agent" "claude" "--workspace" workspace "--output" "json"))
         (routes `((,(safeslop-test--json-key argv) .
                   ((stdout . ,(safeslop-test--fixture "ok-session-create.golden.json"))
                    (exit . 0))))))
    (safeslop-test--with-fake-cli routes
      (should (safeslop-contract-ok-p
               (safeslop-test--await
                (lambda (cb) (safeslop-session-new "claude" workspace cb "" "")))))
      (should (equal (safeslop-test--argv-log-lines) (list (safeslop-test--json-key argv)))))))

(ert-deftest safeslop-test-unsupported-agent-error-code ()
  (let* ((workspace (safeslop-test--repo-root))
         (argv (list "session" "create" "--agent" "cursor" "--workspace" workspace
                     "--environment" "container" "--network" "deny" "--output" "json"))
         (routes `((,(safeslop-test--json-key argv) .
                   ((stdout . ,(safeslop-test--fixture "error-agent-unsupported.golden.json"))
                    (exit . 2))))))
    (safeslop-test--with-fake-cli routes
      (let ((envelope (safeslop-test--await (lambda (cb) (safeslop-session-new "cursor" workspace cb)))))
        (should-not (safeslop-contract-ok-p envelope))
        (should (equal (safeslop-contract-first-error-code envelope) "AGENT_UNSUPPORTED"))
        (should (equal safeslop-last-error "AGENT_UNSUPPORTED"))))))

(ert-deftest safeslop-test-session-stop-revokes-credentials ()
  (let* ((argv '("session" "stop" "--session-id" "sess-1" "--revoke-credentials" "--output" "json"))
         (routes `((,(safeslop-test--json-key argv) .
                   ((stdout . ,(safeslop-test--fixture "ok-minimal.golden.json"))
                    (exit . 0))))))
    (safeslop-test--with-fake-cli routes
      (should (safeslop-contract-ok-p (safeslop-test--await (lambda (cb) (safeslop-session-stop "sess-1" cb)))))
      (should (equal (safeslop-test--argv-log-lines) (list (safeslop-test--json-key argv)))))))

(ert-deftest safeslop-test-workspace-path-never-shell-expanded ()
  (let* ((tmp (make-temp-file "safeslop a;b $(touch pwn)" t))
         (workspace (expand-file-name tmp))
         (pwn (expand-file-name "pwn" (file-name-directory (directory-file-name workspace))))
         (argv (list "session" "create" "--agent" "claude" "--workspace" workspace
                     "--environment" "container" "--network" "deny" "--output" "json"))
         (routes `((,(safeslop-test--json-key argv) .
                   ((stdout . ,(safeslop-test--fixture "ok-session-create.golden.json"))
                    (exit . 0))))))
    (unwind-protect
        (safeslop-test--with-fake-cli routes
          (should (safeslop-contract-ok-p (safeslop-test--await (lambda (cb) (safeslop-session-new "claude" workspace cb)))))
          (should-not (file-exists-p pwn))
          (should (equal (safeslop-test--argv-log-lines) (list (safeslop-test--json-key argv)))))
      (delete-directory tmp t))))

(ert-deftest safeslop-test-session-new-from-profile-exact-argv ()
  (let* ((argv '("session" "create" "--profile" "review" "--output" "json"))
         (routes `((,(safeslop-test--json-key argv) .
                   ((stdout . "{\"schema_version\":1,\"ok\":true,\"data\":{\"session_id\":\"sess-profile\",\"profile\":\"review\",\"agent\":\"pi\",\"environment\":\"container\",\"network\":\"deny\",\"workspace\":\"/workspace/project\",\"status\":\"created\",\"created_at\":\"2026-06-26T00:00:00Z\",\"updated_at\":\"2026-06-26T00:00:00Z\",\"credentials_revoked\":false,\"recipeID\":\"abc123def456\",\"image\":\"local/safeslop-tools:abc123def456\"},\"warnings\":[],\"errors\":[]}")
                    (exit . 0))))))
    (safeslop-test--with-fake-cli routes
      (let ((envelope (safeslop-test--await
                       (lambda (cb) (safeslop-session-new-from-profile "review" cb)))))
        (should (safeslop-contract-ok-p envelope))
        (should (equal (safeslop-test--argv-log-lines) (list (safeslop-test--json-key argv))))))))

(ert-deftest safeslop-test-session-profile-args ()
  (should (equal (safeslop-session--create-profile-args "review")
                 '("session" "create" "--profile" "review" "--output" "json"))))

(ert-deftest safeslop-test-session-profile-create-progress-buffer ()
  "Profile-backed creation shows a live progress buffer while the CLI is still running."
  (let* ((argv '("session" "create" "--profile" "review" "--output" "json"))
         (routes `((,(safeslop-test--json-key argv) .
                   ((stderr . "building local/safeslop-tools:abc123def456\n")
                    (sleep . 0.2)
                    (stdout . "{\"schema_version\":1,\"ok\":true,\"data\":{\"session_id\":\"sess-profile\"},\"warnings\":[],\"errors\":[]}")
                    (exit . 0))))))
    (when (get-buffer safeslop-session-progress-buffer-name)
      (kill-buffer safeslop-session-progress-buffer-name))
    (safeslop-test--with-fake-cli routes
      (let (done envelope)
        (safeslop-session-new-from-profile
         "review" (lambda (env) (setq envelope env done t)))
        (with-timeout (5 (error "progress buffer did not receive build output"))
          (while (not (and (get-buffer safeslop-session-progress-buffer-name)
                           (with-current-buffer safeslop-session-progress-buffer-name
                             (string-match-p "building local/safeslop-tools" (buffer-string)))))
            (accept-process-output nil 0.05)))
        (with-current-buffer safeslop-session-progress-buffer-name
          (should (derived-mode-p 'safeslop-progress-mode))
          (let ((s (buffer-string)))
            (should (string-match-p "session create --profile review" s))
            (should (string-match-p "building local/safeslop-tools:abc123def456" s))
            (should (string-match-p "running" s))))
        (with-timeout (5 (error "session create did not finish"))
          (while (not done)
            (accept-process-output nil 0.05)))
        (should (safeslop-contract-ok-p envelope))
        (with-current-buffer safeslop-session-progress-buffer-name
          (should (string-match-p "finished successfully (exit 0)" (buffer-string))))))))

(ert-deftest safeslop-test-session-profile-progress-reports-nonzero-exit ()
  "A failed profile-backed create reports the exit status in the progress buffer."
  (let* ((argv '("session" "create" "--profile" "bad" "--output" "json"))
         (routes `((,(safeslop-test--json-key argv) .
                   ((stderr . "build failed\n")
                    (stdout . "{\"schema_version\":1,\"ok\":false,\"data\":{},\"warnings\":[],\"errors\":[{\"code\":\"INVALID_ARGUMENT\",\"message\":\"bad profile\",\"retryable\":false}]}")
                    (exit . 2))))))
    (when (get-buffer safeslop-session-progress-buffer-name)
      (kill-buffer safeslop-session-progress-buffer-name))
    (safeslop-test--with-fake-cli routes
      (let ((envelope (safeslop-test--await
                       (lambda (cb) (safeslop-session-new-from-profile "bad" cb)))))
        (should-not (safeslop-contract-ok-p envelope))
        (should (equal (safeslop-contract-first-error-code envelope) "INVALID_ARGUMENT"))
        (with-current-buffer safeslop-session-progress-buffer-name
          (let ((s (buffer-string)))
            (should (string-match-p "build failed" s))
            (should (string-match-p "failed (exit 2)" s))))))))

(ert-deftest safeslop-test-session-attach-uses-term-pty-exact-argv ()
  (let* ((argv '("session" "run" "--session-id" "sess-term"))
         (routes `((,(safeslop-test--json-key argv) .
                   ((stdout . "")
                    (exit . 0))))))
    (safeslop-test--with-fake-cli routes
      (let ((buf (safeslop-session-attach "sess-term")))
        (with-current-buffer buf
          (should (derived-mode-p 'term-mode))
          (let ((proc (get-buffer-process buf)))
            (while (and proc (process-live-p proc))
              (accept-process-output proc 0.1))))
        (should (equal (safeslop-test--argv-log-lines) (list (safeslop-test--json-key argv))))))))

(ert-deftest safeslop-test-session-run-detached-args ()
  (should (equal (safeslop-session--run-detached-args "sess-detached")
                 '("session" "run" "--session-id" "sess-detached" "--detach")))
  (should-not (member "--output" (safeslop-session--run-detached-args "sess-detached"))))

(ert-deftest safeslop-test-session-id-candidates-from-list ()
  (let ((env (safeslop-contract-parse-string
              (concat "{\"schema_version\":1,\"ok\":true,\"data\":{\"sessions\":"
                      "[{\"session_id\":\"sess-a\"},{\"session_id\":\"sess-b\"}]},"
                      "\"warnings\":[],\"errors\":[]}"))))
    (should (equal (safeslop-session--session-id-candidates env) '("sess-a" "sess-b")))))

(ert-deftest safeslop-test-reattach-uses-attach-argv ()
  "Reattach builds `session attach --session-id ...' argv under a term PTY
\(specs/0051 PR4): the detached session is rejoined over its socket, not re-run."
  (let* ((argv '("session" "attach" "--session-id" "sess-reattach"))
         (routes `((,(safeslop-test--json-key argv) .
                   ((stdout . "")
                    (exit . 0))))))
    (safeslop-test--with-fake-cli routes
      (let ((buf (safeslop-session-reattach "sess-reattach")))
        (with-current-buffer buf
          (should (derived-mode-p 'term-mode))
          (let ((proc (get-buffer-process buf)))
            (while (and proc (process-live-p proc))
              (accept-process-output proc 0.1))))
        (should (equal (safeslop-test--argv-log-lines) (list (safeslop-test--json-key argv))))))))

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

(ert-deftest safeslop-test-pty-unavailable-triggers-jsonl-fallback ()
  "Go->Emacs round trip: when `session run' emits the PTY_UNAVAILABLE envelope,
attach must switch to the read-only JSONL status fallback for the same session
\(specs/0050 PR4).  The fake CLI replays the Go golden over the term PTY and the
status route, and the argv log proves both the run and the keyed fallback ran."
  (let* ((run-argv '("session" "run" "--session-id" "sess-pty"))
         (status-argv '("session" "status" "--session-id" "sess-pty" "--output" "jsonl"))
         (routes `((,(safeslop-test--json-key run-argv) .
                   ((stdout . ,(safeslop-test--fixture "error-pty-unavailable.golden.json"))
                    (exit . 1)))
                   (,(safeslop-test--json-key status-argv) .
                   ((stdout . "{\"schema_version\":1,\"ok\":true,\"data\":{},\"warnings\":[],\"errors\":[]}\n")
                    (exit . 0))))))
    (safeslop-test--with-fake-cli routes
      (let ((run-buf (safeslop-session-attach "sess-pty")))
        ;; drive the run to completion so its sentinel keys on PTY_UNAVAILABLE
        (let ((proc (get-buffer-process run-buf)))
          (while (and proc (process-live-p proc))
            (accept-process-output proc 0.1)))
        ;; the sentinel launches the JSONL fallback; wait for the buffer (bounded)
        (let ((tries 0))
          (while (and (not (get-buffer "*safeslop session status jsonl*"))
                      (< tries 50))
            (accept-process-output nil 0.1)
            (setq tries (1+ tries))))
        (let ((fb (get-buffer "*safeslop session status jsonl*")))
          (should fb)
          (with-current-buffer fb
            (should (derived-mode-p 'compilation-mode))
            (let ((fproc (get-buffer-process fb)))
              (while (and fproc (process-live-p fproc))
                (accept-process-output fproc 0.1)))))
        ;; both legs ran, in order: the run first, then the keyed status fallback
        (should (equal (safeslop-test--argv-log-lines)
                       (list (safeslop-test--json-key run-argv)
                             (safeslop-test--json-key status-argv))))))))

(provide 'safeslop-contract-test)
;;; safeslop-contract-test.el ends here
