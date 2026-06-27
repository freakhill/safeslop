;;; safeslop-test.el --- Smoke tests for safeslop.el -*- lexical-binding: t; -*-

(require 'ert)
(require 'cl-lib)
(require 'safeslop)
(require 'safeslop-doom)

(ert-deftest safeslop-test-loads-core-commands ()
  (dolist (fn '(safeslop-doctor
                safeslop-policy-check-file
                safeslop-session-new
                safeslop-session-attach
                safeslop-session-list
                safeslop-session-status
                safeslop-session-stop
                safeslop-session-reattach
                safeslop-switch-to-session-buffer
                safeslop-show-last-error
                safeslop-help))
    (should (fboundp fn))))

(ert-deftest safeslop-test-default-keymap-has-spec-bindings ()
  (safeslop-bind-default-keys)
  (should (eq (lookup-key global-map (kbd "C-c s D")) #'safeslop-daemon-start))
  (should (eq (lookup-key global-map (kbd "C-c s d")) #'safeslop-doctor))
  (should (eq (lookup-key global-map (kbd "C-c s p")) #'safeslop-policy-check-file))
  (should (eq (lookup-key global-map (kbd "C-c s n")) #'safeslop-session-new))
  (should (eq (lookup-key global-map (kbd "C-c s ?")) #'safeslop-help)))

(ert-deftest safeslop-test-doom-shim-loads-without-doom ()
  (should (featurep 'safeslop-doom))
  (should (or (not (fboundp 'map!))
              (memq (safeslop-doom-bind-leader) '(t nil)))))

(ert-deftest safeslop-test-daemon-default-socket-lives-under-application-support ()
  (let ((safeslop-daemon-state-dir "~/Library/Application Support/safeslop")
        (safeslop-daemon-socket nil))
    (should (string-suffix-p "Library/Application Support/safeslop/safeslop.sock"
                             (safeslop-daemon-socket-path)))))

(ert-deftest safeslop-test-daemon-command-uses-state-dir-and-socket ()
  (let* ((tmp (make-temp-file "safeslop-daemon" t))
         (safeslop-daemon-state-dir tmp)
         (safeslop-daemon-socket nil)
         (safeslop-daemon-args '("serve" "--debug")))
    (should (equal (safeslop--daemon-command "/bin/echo")
                   (list "/bin/echo"
                         "--state-dir" (file-name-as-directory tmp)
                         "--socket" (expand-file-name "safeslop.sock" tmp)
                         "serve" "--debug")))))

(ert-deftest safeslop-test-ensure-daemon-no-binary-is-nonfatal ()
  (let ((safeslop-daemon-program "/definitely/not/a/safeslopd")
        (safeslop-autostart-daemon t)
        (process-environment (cons "SAFESLOP_DAEMON_BIN=" process-environment))
        (exec-path nil))
    (should-not (safeslop--ensure-daemon))))

(ert-deftest safeslop-test-output-mode-has-evil-normal-bindings ()
  (let (initial-states)
    (cl-letf (((symbol-function 'evil-set-initial-state)
               (lambda (mode state) (push (list mode state) initial-states)))
              ((symbol-function 'evil-define-key)
               (lambda (_state keymap key def &rest bindings)
                 (define-key keymap key def)
                 (while bindings
                   (define-key keymap (pop bindings) (pop bindings))))))
      (unless (featurep 'evil)
        (provide 'evil))
      ;; Both the output buffers and the portal dashboard enter Evil normal state.
      (should (member '(safeslop-output-mode normal) initial-states))
      (should (member '(safeslop-portal-mode normal) initial-states))
      (should (eq (lookup-key safeslop-output-mode-map (kbd "g")) #'safeslop-doctor))
      (should (eq (lookup-key safeslop-output-mode-map (kbd "q")) #'quit-window))
      (should (eq (lookup-key safeslop-portal-mode-map (kbd "k")) #'safeslop-portal-stop)))))

;;; safeslop-test.el ends here

;;; Portal + debug + data-rendering tests ------------------------------------

(ert-deftest safeslop-test-portal-and-debug-commands-load ()
  (dolist (fn '(safeslop-portal safeslop-portal-refresh safeslop-portal-open
                safeslop-portal-stop safeslop-debug-log))
    (should (fboundp fn)))
  (should (eq (symbol-function 'safeslop) 'safeslop-portal)))

(ert-deftest safeslop-test-keymap-has-portal-and-debug ()
  (safeslop-bind-default-keys)
  (should (eq (lookup-key global-map (kbd "C-c s P")) #'safeslop-portal))
  (should (eq (lookup-key global-map (kbd "C-c s L")) #'safeslop-debug-log)))

(ert-deftest safeslop-test-portal-keymap-actions ()
  (should (eq (lookup-key safeslop-portal-mode-map (kbd "RET")) #'safeslop-portal-open))
  (should (eq (lookup-key safeslop-portal-mode-map (kbd "k")) #'safeslop-portal-stop))
  (should (eq (lookup-key safeslop-portal-mode-map (kbd "g")) #'safeslop-portal-refresh))
  (should (eq (lookup-key safeslop-portal-mode-map (kbd "L")) #'safeslop-debug-log)))

(ert-deftest safeslop-test-portal-rows-from-sessions ()
  "`safeslop-portal--rows' builds id + columns from a parsed session list."
  (let ((envelope (safeslop-contract-parse-string
                   (concat "{\"schema_version\":1,\"ok\":true,\"data\":{\"sessions\":"
                           "[{\"session_id\":\"sess-abc123\",\"agent\":\"claude\","
                           "\"environment\":\"sandbox\",\"network\":\"deny\","
                           "\"status\":\"running\",\"workspace\":\"/tmp/ws\"}]},"
                           "\"warnings\":[],\"errors\":[]}"))))
    (cl-letf (((symbol-function 'safeslop--call-json) (lambda (_args) envelope)))
      (let* ((rows (safeslop-portal--rows))
             (row (car rows))
             (cols (cadr row)))
        (should (= (length rows) 1))
        (should (equal (car row) "sess-abc123"))
        (should (equal (aref cols 1) "claude"))
        (should (equal (aref cols 2) "sandbox"))
        (should (equal (aref cols 3) "deny"))
        (should (equal (aref cols 4) "running"))))))

(ert-deftest safeslop-test-scalar-json-sentinels ()
  (should (equal (safeslop--scalar t) "true"))
  (should (equal (safeslop--scalar :json-false) "false"))
  (should (equal (safeslop--scalar :json-null) "null"))
  (should (equal (safeslop--scalar "x") "x"))
  (should (equal (safeslop--scalar 7) "7")))

(ert-deftest safeslop-test-insert-data-renders-fields ()
  "Rendering an envelope's data shows the data payload, not just ok."
  (let* ((envelope (safeslop-contract-parse-string
                    (concat "{\"schema_version\":1,\"ok\":true,\"data\":"
                            "{\"session_id\":\"sess-x\",\"status\":\"running\",\"agent\":\"pi\"},"
                            "\"warnings\":[],\"errors\":[]}")))
         (data (safeslop-contract-data envelope)))
    (with-temp-buffer
      (safeslop--insert-data data 0)
      (let ((s (buffer-string)))
        (should (string-match-p "session_id: sess-x" s))
        (should (string-match-p "status: running" s))
        (should (string-match-p "agent: pi" s))))))

(ert-deftest safeslop-test-debug-format-redacts ()
  "`safeslop--debug-format' emits only allowlisted, non-secret fields."
  (let ((line (safeslop--debug-format '(:event call :argv "session list" :secret "TOPSECRET"))))
    (should (string-match-p "event=call" line))
    (should (string-match-p "argv=session list" line))
    (should-not (string-match-p "secret" line))
    (should-not (string-match-p "TOPSECRET" line))))

(ert-deftest safeslop-test-call-json-logs-to-debug ()
  "Each CLI call is recorded in the debug buffer (call + result)."
  (when (get-buffer safeslop-debug-buffer-name)
    (kill-buffer safeslop-debug-buffer-name))
  (cl-letf (((symbol-function 'call-process)
             (lambda (&rest _)
               (insert "{\"schema_version\":1,\"ok\":true,\"data\":{},\"warnings\":[],\"errors\":[]}")
               0)))
    (safeslop--call-json '("doctor" "--json")))
  (with-current-buffer safeslop-debug-buffer-name
    (let ((s (buffer-string)))
      (should (string-match-p "event=call" s))
      (should (string-match-p "argv=doctor --json" s))
      (should (string-match-p "event=result" s)))))

;;; Robust JSON handling (stale/erroring binary) -----------------------------

(ert-deftest safeslop-test-error-envelope-shape ()
  (let ((env (safeslop--error-envelope "X_CODE" "boom")))
    (should-not (safeslop-contract-ok-p env))
    (should (equal (safeslop-contract-first-error-code env) "X_CODE"))
    (should (equal (alist-get 'message (car (safeslop-contract-errors env))) "boom"))
    (should (null (safeslop-contract-data env)))))

(ert-deftest safeslop-test-call-json-handles-non-json-output ()
  "A stale/erroring binary returns non-JSON; the client must degrade, not crash."
  (cl-letf (((symbol-function 'call-process)
             (lambda (&rest _)
               (insert "safeslop: unknown command \"session\" for \"safeslop\"")
               1)))
    (let ((envelope (safeslop--call-json '("session" "list" "--output" "json"))))
      (should-not (safeslop-contract-ok-p envelope))
      (should (equal (safeslop-contract-first-error-code envelope) "CLIENT_NON_JSON"))
      (let ((msg (alist-get 'message (car (safeslop-contract-errors envelope)))))
        (should (string-match-p "did not return JSON" msg))
        (should (string-match-p "unknown command" msg))
        (should (string-match-p "make install" msg))))))

(ert-deftest safeslop-test-call-json-empty-output ()
  "Empty stdout (binary that printed nothing) yields a clear message, not a crash."
  (cl-letf (((symbol-function 'call-process) (lambda (&rest _) 1)))
    (let* ((envelope (safeslop--call-json '("doctor" "--json")))
           (msg (alist-get 'message (car (safeslop-contract-errors envelope)))))
      (should-not (safeslop-contract-ok-p envelope))
      (should (string-match-p "no output" msg)))))

(ert-deftest safeslop-test-portal-surfaces-error ()
  "A failed session list leaves the portal empty and reports the error."
  (let (msgs)
    (cl-letf (((symbol-function 'safeslop--call-json)
               (lambda (_) (safeslop--error-envelope "CLIENT_NON_JSON" "stale binary; run make install")))
              ((symbol-function 'message)
               (lambda (fmt &rest a) (push (apply #'format fmt a) msgs))))
      (should (null (safeslop-portal--rows)))
      (should (cl-some (lambda (m) (string-match-p "stale binary" m)) msgs)))))

;;; Portal header-line shortcuts + help -------------------------------------

(ert-deftest safeslop-test-portal-header-line-shows-shortcuts ()
  "The portal shows its key shortcuts in the window header line."
  (with-temp-buffer
    (safeslop-portal-mode)
    (should (stringp header-line-format))
    (let ((hl (substring-no-properties header-line-format)))
      (should (string-match-p "open" hl))
      (should (string-match-p "stop" hl))
      (should (string-match-p "refresh" hl))
      (should (string-match-p "quit" hl)))
    ;; Column header moved into the buffer, freeing the header line.
    (should-not tabulated-list-use-header-line)))

(ert-deftest safeslop-test-portal-has-help-key ()
  (should (eq (lookup-key safeslop-portal-mode-map (kbd "?")) #'describe-mode)))
