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

(ert-deftest safeslop-test-session-new-claude-code-alias-exact-argv ()
  (let* ((workspace (safeslop-test--repo-root))
         (argv (list "session" "create" "--agent" "claude-code" "--workspace" workspace "--output" "json"))
         (routes `((,(safeslop-test--json-key argv) .
                   ((stdout . ,(safeslop-test--fixture "ok-session-create.golden.json"))
                    (exit . 0))))))
    (safeslop-test--with-fake-cli routes
      (should (safeslop-contract-ok-p (safeslop-session-new "claude-code" workspace)))
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
