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
  (should-not (lookup-key global-map (kbd "C-c s D")))
  (should (eq (lookup-key global-map (kbd "C-c s d")) #'safeslop-doctor))
  (should (eq (lookup-key global-map (kbd "C-c s p")) #'safeslop-policy-check-file))
  (should (eq (lookup-key global-map (kbd "C-c s n")) #'safeslop-session-new))
  (should (eq (lookup-key global-map (kbd "C-c s ?")) #'safeslop-help)))

(ert-deftest safeslop-test-doom-shim-loads-without-doom ()
  (should (featurep 'safeslop-doom))
  (should (or (not (fboundp 'map!))
              (memq (safeslop-doom-bind-leader) '(t nil)))))

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

;;; Portal in-buffer shortcut legend + help (slopmaxx-style) -----------------

(ert-deftest safeslop-test-portal-legend-in-buffer ()
  "The portal renders its shortcut legend as buffer text above the rows."
  (cl-letf (((symbol-function 'safeslop--call-json)
             (lambda (_)
               (safeslop-contract-parse-string
                "{\"schema_version\":1,\"ok\":true,\"data\":{\"sessions\":[]},\"warnings\":[],\"errors\":[]}"))))
    (with-temp-buffer
      (safeslop-portal-mode)
      (safeslop-portal--render)
      (let ((s (buffer-substring-no-properties (point-min) (point-max))))
        (should (string-match-p "open" s))
        (should (string-match-p "stop" s))
        (should (string-match-p "refresh" s))
        (should (string-match-p "quit" s))))))

(ert-deftest safeslop-test-portal-has-help-key ()
  (should (eq (lookup-key safeslop-portal-mode-map (kbd "?")) #'describe-mode)))

;;; Portal status colours + PID column ---------------------------------------

(ert-deftest safeslop-test-portal-status-face ()
  (should (eq (safeslop-portal--status-face "running") 'success))
  (should (eq (safeslop-portal--status-face "stopped") 'shadow))
  (should (eq (safeslop-portal--status-face "created") 'warning))
  (should (eq (safeslop-portal--status-face "exited") 'error))
  (should (eq (safeslop-portal--status-face "weird") 'default)))

(ert-deftest safeslop-test-portal-status-cell-and-pid ()
  "Rows colour the Status cell by status and carry a PID column."
  (let ((envelope (safeslop-contract-parse-string
                   (concat "{\"schema_version\":1,\"ok\":true,\"data\":{\"sessions\":"
                           "[{\"session_id\":\"sess-r\",\"agent\":\"claude\",\"environment\":\"vm\","
                           "\"network\":\"deny\",\"status\":\"running\",\"pid\":4242,\"workspace\":\"/w\"},"
                           "{\"session_id\":\"sess-c\",\"agent\":\"pi\",\"environment\":\"sandbox\","
                           "\"network\":\"deny\",\"status\":\"created\",\"workspace\":\"/w\"}]},"
                           "\"warnings\":[],\"errors\":[]}"))))
    (cl-letf (((symbol-function 'safeslop--call-json) (lambda (_) envelope)))
      (let* ((rows (safeslop-portal--rows))
             (running (cl-find "sess-r" rows :key #'car :test #'equal))
             (created (cl-find "sess-c" rows :key #'car :test #'equal)))
        (should (eq (get-text-property 0 'face (aref (cadr running) 4)) 'success))
        (should (eq (get-text-property 0 'face (aref (cadr created) 4)) 'warning))
        (should (equal (aref (cadr running) 5) "4242"))
        (should (equal (aref (cadr created) 5) "—"))))))
