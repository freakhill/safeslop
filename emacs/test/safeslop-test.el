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
                safeslop-session-remove
                safeslop-session-prune
                safeslop-session-reattach
                safeslop-session-new-from-profile
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
  "Evil tables enter normal state, carry gr/ga, and never shadow motions.
The evil-define-key* stub RECORDS bindings instead of defining them into the
real maps: defining `gr' would turn the raw `g' refresh binding into a prefix."
  (let (initial-states bindings)
    (cl-letf (((symbol-function 'evil-set-initial-state)
               (lambda (mode state) (push (list mode state) initial-states)))
              ((symbol-function 'evil-define-key*)
               (lambda (_state keymap key def &rest more)
                 (push (list keymap (key-description key) def) bindings)
                 (while more
                   (push (list keymap (key-description (pop more)) (pop more))
                         bindings)))))
      (unless (featurep 'evil)
        (provide 'evil))
      ;; Both the output buffers and the portal dashboard enter Evil normal state.
      (should (member '(safeslop-output-mode normal) initial-states))
      (should (member '(safeslop-portal-mode normal) initial-states))
      ;; Refresh rides gr, the portal auto-toggle ga (evil-collection style);
      ;; the shared keys are still applied through Evil.
      (should (member (list safeslop-output-mode-map "g r" #'safeslop-output-refresh) bindings))
      (should (member (list safeslop-portal-mode-map "g r" #'safeslop-portal-refresh) bindings))
      (should (member (list safeslop-portal-mode-map "g a" #'safeslop-portal-toggle-auto-refresh) bindings))
      (should (member (list safeslop-portal-mode-map "s" #'safeslop-portal-stop) bindings))
      (should (member (list safeslop-output-mode-map "d" #'safeslop-doctor) bindings))
      (should (member (list safeslop-output-mode-map "E" #'safeslop-show-last-error) bindings))
      (should (member (list safeslop-output-mode-map "q" #'quit-window) bindings))
      ;; specs/0063 F1: no Evil table binds a bare motion/search key.
      (dolist (motion '("j" "k" "g" "n" "f" "a"))
        (should-not (cl-find-if (lambda (b) (equal (nth 1 b) motion)) bindings))))))

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
  (should (eq (lookup-key safeslop-portal-mode-map (kbd "s")) #'safeslop-portal-stop))
  (should (eq (lookup-key safeslop-portal-mode-map (kbd "g")) #'safeslop-portal-refresh))
  (should (eq (lookup-key safeslop-portal-mode-map (kbd "a")) #'safeslop-portal-toggle-auto-refresh))
  (should (eq (lookup-key safeslop-portal-mode-map (kbd "r")) #'safeslop-portal-run))
  (should (eq (lookup-key safeslop-portal-mode-map (kbd "R")) #'safeslop-portal-run-detached))
  (should (eq (lookup-key safeslop-portal-mode-map (kbd "A")) #'safeslop-portal-reattach))
  (should (eq (lookup-key safeslop-portal-mode-map (kbd "c")) #'safeslop-portal-new))
  (should (eq (lookup-key safeslop-portal-mode-map (kbd "^")) #'safeslop-portal-follow-profile))
  (should (eq (lookup-key safeslop-portal-mode-map (kbd "L")) #'safeslop-debug-log))
  ;; specs/0063 F1/F2: freed keys are really free — k/n/f fall through (Evil
  ;; motions), D no longer detaches on this surface.
  (should-not (lookup-key safeslop-portal-mode-map (kbd "k")))
  (should-not (lookup-key safeslop-portal-mode-map (kbd "n")))
  (should-not (lookup-key safeslop-portal-mode-map (kbd "f")))
  (should-not (lookup-key safeslop-portal-mode-map (kbd "D"))))

(ert-deftest safeslop-test-portal-legend-lists-auto ()
  "The in-buffer legend advertises the auto-refresh toggle."
  (should (string-match-p "auto" (safeslop-portal--legend))))

(ert-deftest safeslop-test-portal-timer-start-and-cancel ()
  "A positive interval starts a repeating timer; cancel tears it down."
  (let ((safeslop-portal-refresh-interval 5)
        (safeslop-portal--timer nil))
    (unwind-protect
        (progn
          (safeslop-portal--start-timer)
          (should (timerp safeslop-portal--timer)))
      (safeslop-portal--cancel-timer))
    (should-not safeslop-portal--timer)))

(ert-deftest safeslop-test-portal-timer-nil-interval-stays-static ()
  "A nil interval leaves the portal static: no timer is created."
  (let ((safeslop-portal-refresh-interval nil)
        (safeslop-portal--timer nil))
    (safeslop-portal--start-timer)
    (should-not safeslop-portal--timer)))

(ert-deftest safeslop-test-portal-auto-refresh-self-cancels-without-buffer ()
  "The timer callback cancels itself once the portal buffer is gone."
  (when (get-buffer safeslop-portal-buffer-name)
    (kill-buffer safeslop-portal-buffer-name))
  (let ((safeslop-portal--timer (run-at-time 100 100 #'ignore)))
    (unwind-protect (safeslop-portal--auto-refresh)
      (safeslop-portal--cancel-timer))
    (should-not safeslop-portal--timer)))

(ert-deftest safeslop-test-portal-rows-from-sessions ()
  "`safeslop-portal--rows' builds id + columns from a parsed session list."
  (let* ((envelope (safeslop-contract-parse-string
                    (concat "{\"schema_version\":1,\"ok\":true,\"data\":{\"sessions\":"
                            "[{\"session_id\":\"sess-abc123\",\"agent\":\"claude\","
                            "\"environment\":\"container\",\"network\":\"deny\","
                            "\"status\":\"running\",\"workspace\":\"/tmp/ws\","
                            "\"recipeID\":\"abc123def456\",\"image\":\"local/safeslop-tools:abc123def456\","
                            "\"resolved\":{\"identitySet\":[\"claude-code\",\"node\",\"pnpm\"]}}]},"
                            "\"warnings\":[],\"errors\":[]}")))
         (rows (safeslop-portal--rows (safeslop-portal--sessions-from envelope)))
         (row (car rows))
         (cols (cadr row)))
    (should (= (length rows) 1))
    (should (equal (car row) "sess-abc123"))
    (should (equal (aref cols 1) "claude"))
    (should (equal (aref cols 2) "container"))
    (should (equal (aref cols 3) "deny"))
    (should (equal (aref cols 4) "running"))
    (should (equal (aref cols 7) "claude-code,node,pnpm"))
    (should (equal (aref cols 8) "abc123def456"))))

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
  "A failed session list echoes the error AND leaves the persistent in-buffer
banner (shared engine error state) pointing at g/d/E/L — never a silently
mysterious bare table."
  (let (msgs)
    (cl-letf (((symbol-function 'message)
               (lambda (fmt &rest a) (push (apply #'format fmt a) msgs)))
              ((symbol-function 'safeslop--call-json-async)
               (lambda (_args cb)
                 (funcall cb (safeslop--error-envelope
                              "CLIENT_NON_JSON" "stale binary; run make install")))))
      (with-temp-buffer
        (safeslop-portal-mode)
        (safeslop-portal--render)
        (should (null (safeslop-portal--sessions-from
                       (safeslop--error-envelope "CLIENT_NON_JSON" "x"))))
        (let ((s (buffer-substring-no-properties (point-min) (point-max))))
          (should (string-match-p "session list failed" s))
          (should (string-match-p "stale binary" s))
          (should (string-match-p "retry" s)))
        (should (cl-some (lambda (m) (string-match-p "stale binary" m)) msgs))))))

;;; Portal in-buffer shortcut legend + help (slopmaxx-style) -----------------

(ert-deftest safeslop-test-portal-legend-in-buffer ()
  "The portal renders its shortcut legend as buffer text above the rows."
  (cl-letf (((symbol-function 'safeslop--call-json-async)
             (lambda (_args cb)
               (funcall cb (safeslop-contract-parse-string
                            "{\"schema_version\":1,\"ok\":true,\"data\":{\"sessions\":[]},\"warnings\":[],\"errors\":[]}")))))
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

(ert-deftest safeslop-test-portal-net-cell ()
  "Network cells preserve text and add colour-redundant safety hints."
  (let ((allow (safeslop-portal--net-cell "allow"))
        (deny (safeslop-portal--net-cell "deny")))
    (should (equal allow "allow"))
    (should (equal deny "deny"))
    (should (eq (get-text-property 0 'face allow) 'safeslop-net-allow))
    (should (eq (get-text-property 0 'face deny) 'safeslop-net-deny))
    (should (get-text-property 0 'help-echo allow))))

(ert-deftest safeslop-test-portal-primary-action-by-state ()
  (should (eq (safeslop-portal--primary-action "created" "") 'run))
  (should (eq (safeslop-portal--primary-action "running" "/tmp/s.sock") 'reattach))
  (should (eq (safeslop-portal--primary-action "running" "") 'live))
  (should (eq (safeslop-portal--primary-action "stopped" "") 'status)))

(ert-deftest safeslop-test-portal-status-cell-and-pid ()
  "Rows colour the Status cell by status and carry a PID column."
  (let ((envelope (safeslop-contract-parse-string
                   (concat "{\"schema_version\":1,\"ok\":true,\"data\":{\"sessions\":"
                           "[{\"session_id\":\"sess-r\",\"agent\":\"claude\",\"environment\":\"container\","
                           "\"network\":\"deny\",\"status\":\"running\",\"pid\":4242,\"workspace\":\"/w\"},"
                           "{\"session_id\":\"sess-c\",\"agent\":\"pi\",\"environment\":\"host\","
                           "\"network\":\"deny\",\"status\":\"created\",\"workspace\":\"/w\"}]},"
                           "\"warnings\":[],\"errors\":[]}"))))
    (let* ((rows (safeslop-portal--rows (safeslop-portal--sessions-from envelope)))
           (running (cl-find "sess-r" rows :key #'car :test #'equal))
           (created (cl-find "sess-c" rows :key #'car :test #'equal)))
      (should (eq (get-text-property 0 'face (aref (cadr running) 4)) 'success))
      (should (eq (get-text-property 0 'face (aref (cadr created) 4)) 'warning))
      (should (eq (get-text-property 0 'face (aref (cadr running) 3)) 'safeslop-net-deny))
      (should (equal (aref (cadr running) 5) "4242"))
      (should (equal (aref (cadr created) 5) "—")))))

(ert-deftest safeslop-test-portal-recipe-and-image-cells ()
  "Recipe lists resolved packages; Image shows the recipeID tag."
  (let ((sess '((recipeID . "abc123def456")
                (image . "local/safeslop-tools:abc123def456")
                (resolved . ((identitySet . ["node" "pi" "pnpm"]))))))
    (should (equal (safeslop-portal--recipe-cell sess) "node,pi,pnpm"))
    (should (equal (safeslop-portal--image-cell sess) "abc123def456"))))

;;; Portal Age column --------------------------------------------------------

(ert-deftest safeslop-test-portal-humanize-age ()
  (should (equal (safeslop-portal--humanize-age 5) "now"))
  (should (equal (safeslop-portal--humanize-age 90) "1m"))
  (should (equal (safeslop-portal--humanize-age 7200) "2h"))
  (should (equal (safeslop-portal--humanize-age 200000) "2d")))

(ert-deftest safeslop-test-portal-age-cell ()
  (should (equal (safeslop-portal--age '((updated_at . ""))) "—"))
  (should (equal (safeslop-portal--age '((updated_at . "not-a-time"))) "—"))
  (should-not (equal (safeslop-portal--age '((updated_at . "2026-06-28T00:00:00Z"))) "—")))

;;; Surface navigation (specs/0052 M0) ---------------------------------------

(ert-deftest safeslop-test-surface-universal-keys ()
  (should (eq (lookup-key safeslop-surface-mode-map (kbd "d")) #'safeslop-doctor))
  (should (eq (lookup-key safeslop-surface-mode-map (kbd "E")) #'safeslop-show-last-error))
  (should (eq (lookup-key safeslop-surface-mode-map (kbd "L")) #'safeslop-debug-log))
  (should (eq (lookup-key safeslop-surface-mode-map (kbd "?")) #'describe-mode))
  (should (eq (lookup-key safeslop-surface-mode-map (kbd "q")) #'quit-window)))

(ert-deftest safeslop-test-output-safe-rerun-predicate ()
  (should (safeslop--safe-rerun-p '("validate" "safeslop.cue" "--json")))
  (should (safeslop--safe-rerun-p '("session" "status" "--session-id" "sess-x" "--output" "json")))
  (should-not (safeslop--safe-rerun-p '("profile" "create" "--output" "json"))))

(ert-deftest safeslop-test-surface-order-has-three ()
  (should (= (length safeslop-surface--order) 3))
  (should (equal (mapcar #'car safeslop-surface--order) '(sessions profiles credentials))))

(ert-deftest safeslop-test-surface-tab-strip-shows-switch-keys ()
  "The tab strip advertises the direct switch key before each label and the cycle
keys after, so changing surface is discoverable in the strip itself."
  (let ((strip (substring-no-properties (safeslop-surface--tab-strip 'sessions))))
    (should (string-match-p "P Sessions" strip))
    (should (string-match-p "F Profiles" strip))
    (should (string-match-p "K Credentials" strip))
    (should (string-match-p "cycle surface" strip))))

(ert-deftest safeslop-test-surface-tab-and-cycle-keys ()
  "TAB/backtab and [/] cycle surfaces from every dashboard's shared parent map."
  (should (eq (lookup-key safeslop-surface-mode-map (kbd "TAB")) #'safeslop-surface-next))
  (should (eq (lookup-key safeslop-surface-mode-map (kbd "<backtab>")) #'safeslop-surface-prev))
  (should (eq (lookup-key safeslop-surface-mode-map (kbd "]")) #'safeslop-surface-next))
  (should (eq (lookup-key safeslop-surface-mode-map (kbd "[")) #'safeslop-surface-prev))
  ;; Reachable through each surface's own keymap (portal binds TAB directly; the
  ;; others inherit it via the parent).
  (should (eq (lookup-key safeslop-portal-mode-map (kbd "TAB")) #'safeslop-surface-next))
  (should (eq (lookup-key safeslop-profiles-mode-map (kbd "TAB")) #'safeslop-surface-next)))

(ert-deftest safeslop-test-surface-step-cycles ()
  "`safeslop-surface--step' calls the next/prev surface command, wrapping around."
  (let (called)
    (cl-letf (((symbol-function 'safeslop-portal) (lambda () (interactive) (setq called 'sessions)))
              ((symbol-function 'safeslop-profiles) (lambda () (interactive) (setq called 'profiles)))
              ((symbol-function 'safeslop-credentials) (lambda () (interactive) (setq called 'credentials))))
      ;; No safeslop surface is current in a temp buffer, so step starts from the
      ;; first surface (sessions); with three surfaces [sessions profiles
      ;; credentials], +1 reaches profiles and -1 wraps to credentials.
      (with-temp-buffer
        (safeslop-surface--step 1)
        (should (eq called 'profiles))
        (setq called nil)
        (safeslop-surface--step -1)
        (should (eq called 'credentials))))))

(ert-deftest safeslop-test-surface-restore-views-preserves-scroll-and-point ()
  "`restore-views' puts each window's scroll back and syncs the cursor to POINT,
clamping to a now-shorter buffer instead of erroring — the core of the cursor-jump
fix."
  (let (set-points set-starts)
    (cl-letf (((symbol-function 'window-live-p) (lambda (_w) t))
              ((symbol-function 'set-window-point)
               (lambda (w p) (push (cons w p) set-points)))
              ((symbol-function 'set-window-start)
               (lambda (w s &optional _noforce) (push (cons w s) set-starts))))
      (with-temp-buffer
        (insert (make-string 50 ?x))       ; point-max = 51
        (safeslop-surface--restore-views '((winA 999 40) (winB 10 5)) 999)
        ;; POINT (999) is clamped to point-max (51) for both windows.
        (should (equal (alist-get 'winA set-points) 51))
        (should (equal (alist-get 'winB set-points) 51))
        ;; Each window's captured scroll start is restored (clamped).
        (should (equal (alist-get 'winA set-starts) 40))
        (should (equal (alist-get 'winB set-starts) 5))))))

(ert-deftest safeslop-test-surface-tab-strip ()
  "The tab strip names both surfaces; the active one is emphasized."
  (let ((strip (safeslop-surface--tab-strip 'profiles)))
    (should (string-match-p "Sessions" strip))
    (should (string-match-p "Profiles" strip))
    (should (eq (get-text-property (string-match "Profiles" strip) 'face strip)
                'mode-line-emphasis))
    ;; an inactive label is a clickable link, not emphasized
    (should (eq (get-text-property (string-match "Sessions" strip) 'face strip)
                'link))))

(ert-deftest safeslop-test-portal-inherits-surface-keys ()
  "The portal keymap inherits the shared surface switch keys."
  (should (eq (lookup-key safeslop-portal-mode-map (kbd "F")) #'safeslop-profiles))
  (should (eq (lookup-key safeslop-portal-mode-map (kbd "]")) #'safeslop-surface-next))
  ;; the portal's own keys still win over the parent
  (should (eq (lookup-key safeslop-portal-mode-map (kbd "s")) #'safeslop-portal-stop)))

;;; Isolation-tier colour (specs/0052 #5) ------------------------------------

(ert-deftest safeslop-test-portal-env-face ()
  (should (eq (safeslop-portal--env-face "host") 'safeslop-tier-host))
  (should (eq (safeslop-portal--env-face "container") 'safeslop-tier-container))
  (should (eq (safeslop-portal--env-face "") 'default))      ; empty -> no boundary implied (specs/0053)
  (should (eq (safeslop-portal--env-face "weird") 'default)))

(ert-deftest safeslop-test-portal-env-cell ()
  "The Env cell keeps its text, colours by tier, and carries the honest note."
  (let ((host (safeslop-portal--env-cell "host"))
        (container (safeslop-portal--env-cell "container")))
    (should (equal host "host"))            ; text preserved (equal ignores props)
    (should (eq (get-text-property 0 'face host) 'safeslop-tier-host))
    (should (get-text-property 0 'help-echo host))
    (should-not (eq (get-text-property 0 'face host)
                    (get-text-property 0 'face container)))))

(ert-deftest safeslop-test-portal-tier-legend ()
  (let ((legend (safeslop-portal--tier-legend)))
    (should (string-match-p "host=none" legend))
    (should (string-match-p "container=egress-allowlisted" legend))))

;;; Post-create open affordance (specs/0052 #3) ------------------------------

(ert-deftest safeslop-test-session-offer-open-attaches-on-yes ()
  (let (attached)
    (cl-letf (((symbol-function 'y-or-n-p) (lambda (&rest _) t))
              ((symbol-function 'safeslop-session-attach) (lambda (id) (setq attached id))))
      (safeslop-session--offer-open "sess-xyz"))
    (should (equal attached "sess-xyz"))))

(ert-deftest safeslop-test-session-offer-open-declines-on-no ()
  (let (attached)
    (cl-letf (((symbol-function 'y-or-n-p) (lambda (&rest _) nil))
              ((symbol-function 'safeslop-session-attach) (lambda (id) (setq attached id))))
      (safeslop-session--offer-open "sess-xyz"))
    (should (null attached))))

(ert-deftest safeslop-test-portal-goto-id ()
  "`safeslop-portal--goto-id' lands point on the row with the given id."
  (with-temp-buffer
    (safeslop-portal-mode)
    (setq tabulated-list-entries
          '(("sess-1" ["sess-1" "claude" "host" "deny" "running" "1" "now" "—" "—" "—" "/ws"])
            ("sess-2" ["sess-2" "pi" "container" "allow" "created" "2" "now" "—" "—" "—" "/ws"])))
    (tabulated-list-print)
    (should (safeslop-portal--goto-id "sess-2"))
    (should (equal (tabulated-list-get-id) "sess-2"))
    (should-not (safeslop-portal--goto-id "sess-nope"))))

;;; Portal corpse cleanup: remove/prune (session rm/prune) -------------------

(ert-deftest safeslop-test-portal-remove-prune-keys ()
  "The portal binds x to remove one session and X to prune all stopped ones."
  (should (eq (lookup-key safeslop-portal-mode-map (kbd "x")) #'safeslop-portal-remove))
  (should (eq (lookup-key safeslop-portal-mode-map (kbd "X")) #'safeslop-portal-prune)))

(ert-deftest safeslop-test-portal-legend-lists-remove ()
  "The in-buffer legend advertises remove and prune."
  (let ((legend (substring-no-properties (safeslop-portal--legend))))
    (should (string-match-p "remove" legend))
    (should (string-match-p "prune" legend))))

(ert-deftest safeslop-test-session-remove-prune-argv ()
  "Remove/prune build the exact CLI argv (no shell, contract --output json)."
  (should (equal (safeslop-session--remove-args "sess-9")
                 '("session" "rm" "--session-id" "sess-9" "--output" "json")))
  (should (equal (safeslop-session--prune-args)
                 '("session" "prune" "--output" "json"))))

(ert-deftest safeslop-test-portal-remove-refuses-running ()
  "`x' on a running session refuses (stop it first) and never calls remove."
  (let (removed)
    (cl-letf (((symbol-function 'safeslop-portal--session-at-point)
               (lambda () '((session_id . "sess-run") (status . "running"))))
              ((symbol-function 'safeslop-session-remove)
               (lambda (&rest _) (setq removed t))))
      (should-error (safeslop-portal-remove) :type 'user-error)
      (should-not removed))))

(ert-deftest safeslop-test-portal-remove-confirms-then-calls ()
  "`x' on a stopped session confirms, then calls remove with its id."
  (let (removed-id)
    (cl-letf (((symbol-function 'safeslop-portal--session-at-point)
               (lambda () '((session_id . "sess-dead") (status . "stopped"))))
              ((symbol-function 'y-or-n-p) (lambda (&rest _) t))
              ((symbol-function 'safeslop-session-remove)
               (lambda (id &optional _cb _quiet) (setq removed-id id))))
      (safeslop-portal-remove)
      (should (equal removed-id "sess-dead")))))

(ert-deftest safeslop-test-portal-remove-declined-does-nothing ()
  "Declining the confirm leaves the session untouched."
  (let (removed)
    (cl-letf (((symbol-function 'safeslop-portal--session-at-point)
               (lambda () '((session_id . "sess-dead") (status . "stopped"))))
              ((symbol-function 'y-or-n-p) (lambda (&rest _) nil))
              ((symbol-function 'safeslop-session-remove)
               (lambda (&rest _) (setq removed t))))
      (safeslop-portal-remove)
      (should-not removed))))

(ert-deftest safeslop-test-portal-prune-confirms-then-calls ()
  "`X' confirms once, then calls prune."
  (let (pruned)
    (cl-letf (((symbol-function 'y-or-n-p) (lambda (&rest _) t))
              ((symbol-function 'safeslop-session-prune)
               (lambda (&optional _cb _quiet) (setq pruned t))))
      (safeslop-portal-prune)
      (should pruned))))

(ert-deftest safeslop-test-portal-actions-refresh-in-place-without-result-popup ()
  "Portal row actions should not steal the operator's window with result buffers.
They run async, report failure in-place, and refresh the portal in place on success;
the standalone `safeslop-session-*' commands may still show their envelope buffers."
  (let ((ok (safeslop-contract-parse-string
             "{\"schema_version\":1,\"ok\":true,\"data\":{\"removed\":[\"sess-dead\"]},\"warnings\":[],\"errors\":[]}"))
        calls refreshes shown messages)
    (cl-labels ((run (form)
                  (setq calls nil refreshes 0 shown 0 messages nil)
                  (cl-letf (((symbol-function 'safeslop-portal--session-at-point)
                             (lambda () '((session_id . "sess-dead") (status . "stopped"))))
                            ((symbol-function 'yes-or-no-p) (lambda (&rest _) t))
                            ((symbol-function 'y-or-n-p) (lambda (&rest _) t))
                            ((symbol-function 'safeslop-session--fetch-data)
                             (lambda (&rest _) nil))
                            ((symbol-function 'safeslop--call-json-async)
                             (lambda (args cb) (push args calls) (funcall cb ok)))
                            ((symbol-function 'safeslop--show-envelope-buffer)
                             (lambda (&rest _) (setq shown (1+ shown))))
                            ((symbol-function 'safeslop-portal-refresh)
                             (lambda () (setq refreshes (1+ refreshes))))
                            ((symbol-function 'message)
                             (lambda (fmt &rest args) (push (apply #'format fmt args) messages))))
                    (eval form))))
      (run '(safeslop-portal-remove))
      (should (equal (car calls) '("session" "rm" "--session-id" "sess-dead" "--output" "json")))
      (should (= refreshes 1))
      (should (= shown 0))
      (run '(safeslop-portal-prune))
      (should (equal (car calls) '("session" "prune" "--output" "json")))
      (should (= refreshes 1))
      (should (= shown 0))
      (run '(safeslop-portal-stop))
      (should (equal (car calls) '("session" "stop" "--session-id" "sess-dead" "--revoke-credentials" "--output" "json")))
      (should (= refreshes 1))
      (should (= shown 0))
      (run '(safeslop-portal-run-detached))
      (should (equal (car calls) '("session" "run" "--session-id" "sess-dead" "--detach")))
      (should (= refreshes 1))
      (should (= shown 0)))))

;;; Cursor-jump fix: in-place refresh preserves window scroll + point --------

(ert-deftest safeslop-test-portal-refresh-preserves-window-view ()
  "A keep-point re-render restores the showing window's scroll and cursor rather
than collapsing point to the top (the cursor-jump regression)."
  (let ((buf (get-buffer-create "*safeslop portal view test*")))
    (unwind-protect
        (cl-letf (((symbol-function 'safeslop--call-json-async)
                   (lambda (_args cb)
                     (funcall cb (safeslop-contract-parse-string
                                  (concat "{\"schema_version\":1,\"ok\":true,\"data\":{\"sessions\":["
                                          "{\"session_id\":\"sess-1\",\"agent\":\"claude\",\"environment\":\"host\","
                                          "\"network\":\"deny\",\"status\":\"running\",\"workspace\":\"/w\"},"
                                          "{\"session_id\":\"sess-2\",\"agent\":\"pi\",\"environment\":\"host\","
                                          "\"network\":\"deny\",\"status\":\"created\",\"workspace\":\"/w\"}]},"
                                          "\"warnings\":[],\"errors\":[]}"))))))
          (with-current-buffer buf
            (safeslop-portal-mode)
            (safeslop-portal--render)          ; initial fill, lands on first row
            (let* ((win (display-buffer buf))
                   captured)
              (with-selected-window win
                (safeslop-portal--goto-id "sess-2")
                (setq captured (point))
                ;; A keep-point refresh must leave the cursor on sess-2, not row 1.
                (safeslop-portal--render t)
                (should (equal (tabulated-list-get-id) "sess-2"))
                (should (= (point) captured))))))
      (when (get-buffer buf) (kill-buffer buf)))))

(ert-deftest safeslop-test-portal-auto-refresh-skips-pending-input ()
  "The auto-refresh timer skips a tick when the operator has input pending, so a
redraw never lands mid-keystroke and moves point out from under an action key."
  (let ((buf (get-buffer-create safeslop-portal-buffer-name))
        refreshed)
    (unwind-protect
        (cl-letf (((symbol-function 'get-buffer-window) (lambda (&rest _) 'win))
                  ((symbol-function 'active-minibuffer-window) (lambda () nil))
                  ((symbol-function 'input-pending-p) (lambda (&rest _) t))
                  ((symbol-function 'safeslop-portal-refresh) (lambda () (setq refreshed t))))
          (let ((safeslop-portal--auto-paused nil))
            (safeslop-portal--auto-refresh)
            (should-not refreshed)))
      (when (get-buffer buf) (kill-buffer buf)))))

;;; specs/0062: shared render engine states + breadcrumb hygiene --------------

(ert-deftest safeslop-test-surface-breadcrumb-title-drops-flags-and-paths ()
  "Output-buffer titles stay short verb phrases: flags and file paths are dropped."
  (should (equal (safeslop-surface--breadcrumb-title
                  '("validate" "/abs/path/safeslop.cue" "--json"))
                 "validate"))
  (should (equal (safeslop-surface--breadcrumb-title
                  '("session" "status" "--session-id" "sess-x" "--output" "json"))
                 "session status"))
  (should (equal (safeslop-surface--breadcrumb-title
                  '("profile" "show" "dev" "~/safeslop.cue" "--output" "json"))
                 "profile show")))

(ert-deftest safeslop-test-portal-empty-state-guidance ()
  "An empty session list renders persistent guidance (n new / g refresh), not a
bare table with no explanation."
  (cl-letf (((symbol-function 'safeslop--call-json-async)
             (lambda (_args cb)
               (funcall cb (safeslop-contract-parse-string
                            "{\"schema_version\":1,\"ok\":true,\"data\":{\"sessions\":[]},\"warnings\":[],\"errors\":[]}")))))
    (with-temp-buffer
      (safeslop-portal-mode)
      (safeslop-portal--render)
      (should (string-match-p
               "No sessions yet"
               (buffer-substring-no-properties (point-min) (point-max)))))))

(ert-deftest safeslop-test-portal-loading-hint-on-first-open ()
  "While the first fetch is in flight the buffer shows header + loading hint
instead of staying blank (the mocked fetch here never calls back)."
  (cl-letf (((symbol-function 'safeslop--call-json-async)
             (lambda (_args _cb) nil)))
    (with-temp-buffer
      (safeslop-portal-mode)
      (safeslop-portal--render)
      (should safeslop-surface--refresh-in-flight)
      (let ((s (buffer-substring-no-properties (point-min) (point-max))))
        (should (string-match-p "checking sessions" s))
        (should (string-match-p "Sessions" s))))))

(ert-deftest safeslop-test-portal-auto-refresh-skips-in-flight ()
  "The auto-refresh timer skips a tick while a prior async fetch is still in
flight, so slow `session list' calls can't stack up."
  (let ((buf (get-buffer-create safeslop-portal-buffer-name))
        refreshed)
    (unwind-protect
        (progn
          (with-current-buffer buf (setq safeslop-surface--refresh-in-flight t))
          (cl-letf (((symbol-function 'get-buffer-window) (lambda (&rest _) 'win))
                    ((symbol-function 'active-minibuffer-window) (lambda () nil))
                    ((symbol-function 'input-pending-p) (lambda (&rest _) nil))
                    ((symbol-function 'safeslop-portal-refresh) (lambda () (setq refreshed t))))
            (let ((safeslop-portal--auto-paused nil))
              (safeslop-portal--auto-refresh)
              (should-not refreshed))))
      (when (get-buffer buf) (kill-buffer buf)))))

;;; specs/0063: lifecycle sort + unified run confirm ---------------------------

(ert-deftest safeslop-test-portal-status-rank-orders-lifecycle ()
  "Rows sort running < created < stopped < failed-ish < unknown, id tie-break."
  (let* ((mk (lambda (id status) (list (cons 'session_id id) (cons 'status status))))
         (rows (safeslop-portal--rows
                (list (funcall mk "sess-b" "stopped")
                      (funcall mk "sess-a" "failed")
                      (funcall mk "sess-d" "running")
                      (funcall mk "sess-c" "created")
                      (funcall mk "sess-e" "running")))))
    (should (equal (mapcar #'car rows)
                   '("sess-d" "sess-e" "sess-c" "sess-b" "sess-a")))))

(ert-deftest safeslop-test-portal-run-confirms-with-danger-summary ()
  "The portal run path shows the same isolation/network summary as Profiles (F4)."
  (let (prompt attached)
    (cl-letf (((symbol-function 'safeslop-portal--session-at-point)
               (lambda () '((session_id . "sess-new") (status . "created")
                            (agent . "pi") (environment . "container")
                            (network . "deny"))))
              ((symbol-function 'yes-or-no-p)
               (lambda (p) (setq prompt p) t))
              ((symbol-function 'safeslop-session-attach)
               (lambda (id) (setq attached id))))
      (safeslop-portal-run)
      (should (equal attached "sess-new"))
      (should (string-match-p "container" prompt))
      (should (string-match-p "deny" prompt)))))

(ert-deftest safeslop-test-portal-open-run-branch-confirms ()
  "RET on a created session confirms before attaching; declining aborts (F4)."
  (let (attached)
    (cl-letf (((symbol-function 'safeslop-portal--session-at-point)
               (lambda () '((session_id . "sess-new") (status . "created")
                            (agent . "pi") (environment . "container")
                            (network . "deny"))))
              ((symbol-function 'yes-or-no-p) (lambda (&rest _) nil))
              ((symbol-function 'safeslop-session-attach)
               (lambda (id) (setq attached id))))
      (safeslop-portal-open)
      (should-not attached))))

;;; specs/0063: annotated completion, stderr separation, buffer switcher, help

(ert-deftest safeslop-test-session-annotation-includes-agent-status-workspace ()
  (let ((ann (safeslop-session--annotate
              '((session_id . "sess-1") (agent . "pi") (status . "created")
                (workspace . "/tmp/ws")))))
    (should (string-match-p "pi" ann))
    (should (string-match-p "created" ann))
    (should (string-match-p "ws" ann)))
  ;; specs/0065 forward-compat: a name field, when present, is shown too.
  (should (string-match-p "myname"
                          (safeslop-session--annotate
                           '((name . "myname") (agent . "pi"))))))

(ert-deftest safeslop-test-call-json-async-stderr-noise-keeps-json-parse ()
  "F9: stderr noise must not corrupt the stdout envelope parse."
  (let ((safeslop-program "/bin/sh") env done)
    (safeslop--call-json-async
     (list "-c" "echo noisy-stderr >&2; printf '{\"schema_version\":1,\"ok\":true,\"data\":{},\"warnings\":[],\"errors\":[]}'")
     (lambda (e) (setq env e done t)))
    (with-timeout (10 (ert-fail "async call timed out"))
      (while (not done) (accept-process-output nil 0.05)))
    (should (safeslop-contract-ok-p env))))

(ert-deftest safeslop-test-call-json-async-reports-stderr-on-failure ()
  "F9: when the CLI fails without JSON, the first stderr line is surfaced."
  (let ((safeslop-program "/bin/sh") env done)
    (safeslop--call-json-async
     (list "-c" "echo boom-details >&2; exit 3")
     (lambda (e) (setq env e done t)))
    (with-timeout (10 (ert-fail "async call timed out"))
      (while (not done) (accept-process-output nil 0.05)))
    (should-not (safeslop-contract-ok-p env))
    (should (string-match-p "boom-details"
                            (alist-get 'message
                                       (car (safeslop-contract-errors env)))))))

(ert-deftest safeslop-test-switch-to-session-buffer-offers-all-safeslop-buffers ()
  (let ((b1 (get-buffer-create "*safeslop portal*"))
        (b2 (get-buffer-create "*safeslop doctor*"))
        collection)
    (unwind-protect
        (cl-letf (((symbol-function 'completing-read)
                   (lambda (_p coll &rest _) (setq collection coll) "*safeslop doctor*"))
                  ((symbol-function 'pop-to-buffer) (lambda (b) b)))
          (safeslop-switch-to-session-buffer)
          (should (member "*safeslop portal*" collection))
          (should (member "*safeslop doctor*" collection)))
      (kill-buffer b1)
      (kill-buffer b2))))

(ert-deftest safeslop-test-help-reflects-command-map ()
  "F8: the help line is generated from the live command map, so it can't drift."
  (let (msg)
    (cl-letf (((symbol-function 'message)
               (lambda (fmt &rest a) (setq msg (apply #'format fmt a)))))
      (safeslop-help))
    (should (string-match-p "P portal" msg))
    (should (string-match-p "b switch-to-session-buffer" msg))
    (should (string-match-p "L debug-log" msg))))

;;; specs/0065: session naming + rename --------------------------------------

(ert-deftest safeslop-test-session-trust-args ()
  "Trust builds the exact CLI argv for host-approving a policy path (no shell)."
  (should (equal (safeslop-session--trust-args "/w/safeslop.cue")
                 '("trust" "/w/safeslop.cue"))))

(ert-deftest safeslop-test-session-create-host-trust-args ()
  "Ad-hoc host create appends --trust-host only when explicitly requested."
  (let ((workspace "/w"))
    (let ((trusted (condition-case nil
                       (safeslop-session--create-args "claude" workspace "host" "deny" t)
                     (wrong-number-of-arguments nil))))
      (should (equal trusted
                     '("session" "create" "--agent" "claude" "--workspace" "/w"
                       "--environment" "host" "--network" "deny"
                       "--trust-host" "--output" "json"))))
    (should-not (member "--trust-host"
                        (safeslop-session--create-args "claude" workspace "host" "deny")))
    (should-not (member "--trust-host"
                        (safeslop-session--create-args "claude" workspace "container" "deny" t)))))

(ert-deftest safeslop-test-session-new-host-trust-ack-adds-flag ()
  "Interactive ad-hoc host creation asks for acknowledgement, then passes --trust-host."
  (let ((answers '("claude" "host" "deny"))
        prompt
        called)
    (cl-letf (((symbol-function 'safeslop-session--read-profile-choice) (lambda () nil))
              ((symbol-function 'completing-read)
               (lambda (&rest _) (pop answers)))
              ((symbol-function 'read-directory-name) (lambda (&rest _) "/w"))
              ((symbol-function 'yes-or-no-p) (lambda (p) (setq prompt p) t))
              ((symbol-function 'safeslop-session--create-async)
               (lambda (args progress-p _callback)
                 (setq called (list args progress-p)))))
      (call-interactively #'safeslop-session-new))
    (should prompt)
    (should (string-match-p "unconfined" prompt))
    (should (equal called
                   '(("session" "create" "--agent" "claude" "--workspace" "/w"
                      "--environment" "host" "--network" "deny"
                      "--trust-host" "--output" "json")
                     nil)))))

(ert-deftest safeslop-test-session-new-host-trust-decline-aborts-before-cli ()
  "Declining the interactive host acknowledgement aborts before session create."
  (let ((answers '("claude" "host" "deny"))
        called)
    (cl-letf (((symbol-function 'safeslop-session--read-profile-choice) (lambda () nil))
              ((symbol-function 'completing-read)
               (lambda (&rest _) (pop answers)))
              ((symbol-function 'read-directory-name) (lambda (&rest _) "/w"))
              ((symbol-function 'yes-or-no-p) (lambda (&rest _) nil))
              ((symbol-function 'safeslop-session--create-async)
               (lambda (&rest _) (setq called t))))
      (should-error (call-interactively #'safeslop-session-new) :type 'user-error))
    (should-not called)))

(ert-deftest safeslop-test-session-host-trust-required-retries-with-flag-not-safeslop-trust ()
  "Ad-hoc host TRUST_REQUIRED without policy path retries with --trust-host only."
  (let ((args '("session" "create" "--agent" "claude" "--workspace" "/w"
                "--environment" "host" "--network" "deny" "--output" "json"))
        retried
        trust-called
        prompt)
    (cl-letf (((symbol-function 'yes-or-no-p) (lambda (p) (setq prompt p) t))
              ((symbol-function 'safeslop--show-envelope-buffer) (lambda (&rest _) nil))
              ((symbol-function 'safeslop--call-json)
               (lambda (trust-args)
                 (setq trust-called trust-args)
                 (safeslop-contract-parse-string
                  "{\"schema_version\":1,\"ok\":true,\"data\":{},\"warnings\":[],\"errors\":[]}")))
              ((symbol-function 'safeslop-session--create-async)
               (lambda (retry-args progress-p _callback)
                 (setq retried (list retry-args progress-p)))))
      (safeslop-session--handle-create-result
       args nil
       (safeslop-contract-parse-string
        (concat "{\"schema_version\":1,\"ok\":false,\"data\":{},\"warnings\":[],"
                "\"errors\":[{\"code\":\"TRUST_REQUIRED\",\"message\":\"host acknowledgement required\","
                "\"retryable\":false,\"details\":{\"environment\":\"host\",\"hint\":\"add --trust-host\"}}]}"))))
    (should prompt)
    (should (string-match-p "--trust-host" prompt))
    (should-not trust-called)
    (should (equal retried
                   '(("session" "create" "--agent" "claude" "--workspace" "/w"
                      "--environment" "host" "--network" "deny"
                      "--trust-host" "--output" "json")
                     nil)))))

(ert-deftest safeslop-test-session-host-trust-does-not-change-profile-policy-trust ()
  "Profile TRUST_REQUIRED with a policy path still uses safeslop trust, not --trust-host."
  (let ((trusted nil)
        (retried nil))
    (cl-letf (((symbol-function 'y-or-n-p) (lambda (&rest _) t))
              ((symbol-function 'yes-or-no-p) (lambda (&rest _) (error "unexpected host trust prompt")))
              ((symbol-function 'safeslop--show-envelope-buffer) (lambda (&rest _) nil))
              ((symbol-function 'safeslop--call-json)
               (lambda (args)
                 (setq trusted args)
                 (safeslop-contract-parse-string
                  "{\"schema_version\":1,\"ok\":true,\"data\":{},\"warnings\":[],\"errors\":[]}")))
              ((symbol-function 'safeslop-session--create-async)
               (lambda (args _p _cb) (setq retried args))))
      (safeslop-session--handle-create-result
       '("session" "create" "--profile" "dev" "--output" "json") nil
       (safeslop-contract-parse-string
        (concat "{\"schema_version\":1,\"ok\":false,\"data\":{},\"warnings\":[],"
                "\"errors\":[{\"code\":\"TRUST_REQUIRED\",\"message\":\"policy trust required\","
                "\"retryable\":false,\"details\":{\"path\":\"/w/safeslop.cue\"}}]}"))))
    (should (equal trusted '("trust" "/w/safeslop.cue")))
    (should (equal retried '("session" "create" "--profile" "dev" "--output" "json")))))

(ert-deftest safeslop-test-session-create-progress-p ()
  "Spinner-worthy: profile and container ad-hoc creates; not host ad-hoc creates."
  (should (safeslop-session--create-progress-p
           '("session" "create" "--profile" "dev" "--output" "json")))
  (should (safeslop-session--create-progress-p
           '("session" "create" "--agent" "claude" "--environment" "container" "--workspace" "/w")))
  ;; environment omitted -> container default -> still spinner-worthy
  (should (safeslop-session--create-progress-p
           '("session" "create" "--agent" "claude" "--workspace" "/w")))
  (should-not (safeslop-session--create-progress-p
               '("session" "create" "--agent" "claude" "--environment" "host" "--workspace" "/w"))))

(ert-deftest safeslop-test-session-runtime-preflight-shadowed-docker-aborts-attach-before-terminal ()
  "A shadowed docker helper aborts attach before terminal/subprocess launch."
  (let ((calls nil)
        (terminal-called nil)
        (message nil))
    (cl-letf (((symbol-function 'safeslop--call-json)
               (lambda (args)
                 (push args calls)
                 (cond
                  ((equal args '("session" "status" "--session-id" "sess-shadow" "--output" "json"))
                   (safeslop-contract-parse-string
                    "{\"schema_version\":1,\"ok\":true,\"data\":{\"session_id\":\"sess-shadow\",\"environment\":\"container\"},\"warnings\":[],\"errors\":[]}"))
                  ((equal args '("doctor" "--json"))
                   (safeslop-contract-parse-string
                    (concat "{\"schema_version\":1,\"ok\":true,\"data\":{\"tools\":{\"docker\":{"
                            "\"present\":false,\"path\":\"/safe/bin/docker\","
                            "\"shadowed_paths\":[\"/usr/local/bin/docker\"],"
                            "\"secret\":\"op://vault/item/token\"}}},\"warnings\":[],\"errors\":[]}")))
                  (t (error "unexpected argv: %S" args)))))
              ((symbol-function 'safeslop-session--make-terminal)
               (lambda (&rest _)
                 (setq terminal-called t)
                 (get-buffer-create "*unexpected safeslop terminal*")))
              ((symbol-function 'pop-to-buffer) (lambda (buf &rest _) buf)))
      (unwind-protect
          (condition-case err
              (progn
                (safeslop-session-attach "sess-shadow")
                (ert-fail "expected shadowed docker preflight to abort"))
            (user-error (setq message (cadr err))))
        (when (get-buffer "*unexpected safeslop terminal*")
          (kill-buffer "*unexpected safeslop terminal*"))))
    (should-not terminal-called)
    (should (equal (nreverse calls)
                   '(("session" "status" "--session-id" "sess-shadow" "--output" "json")
                     ("doctor" "--json"))))
    (should (string-match-p "/safe/bin/docker" message))
    (should (string-match-p "/usr/local/bin/docker" message))
    (should-not (string-match-p "op://\\|token" message))))

(ert-deftest safeslop-test-session-create-trust-required-retries ()
  "A TRUST_REQUIRED refusal offers to trust the policy and re-dispatches the create."
  (let ((trusted nil) (retried nil))
    (cl-letf (((symbol-function 'y-or-n-p) (lambda (&rest _) t))
              ((symbol-function 'safeslop--show-envelope-buffer) (lambda (&rest _) nil))
              ((symbol-function 'safeslop--call-json)
               (lambda (args)
                 (setq trusted args)
                 (safeslop-contract-parse-string
                  "{\"schema_version\":1,\"ok\":true,\"data\":{},\"warnings\":[],\"errors\":[]}")))
              ((symbol-function 'safeslop-session--create-async)
               (lambda (args _p _cb) (setq retried args))))
      (let ((envelope (safeslop-contract-parse-string
                       (concat "{\"schema_version\":1,\"ok\":false,\"data\":{},\"warnings\":[],"
                               "\"errors\":[{\"code\":\"TRUST_REQUIRED\",\"message\":\"nope\","
                               "\"retryable\":false,\"details\":{\"path\":\"/w/safeslop.cue\"}}]}"))))
        (safeslop-session--handle-create-result
         '("session" "create" "--profile" "dev" "--output" "json") nil envelope)))
    (should (equal trusted '("trust" "/w/safeslop.cue")))
    (should (equal retried '("session" "create" "--profile" "dev" "--output" "json")))))

(ert-deftest safeslop-test-session-rename-args ()
  "Rename builds the exact CLI argv (no shell, contract --output json)."
  (should (equal (safeslop-session--rename-args "sess-9" "my label")
                 '("session" "rename" "--session-id" "sess-9"
                   "--name" "my label" "--output" "json")))
  ;; Empty input clears: the empty name flows straight through to argv.
  (should (equal (safeslop-session--rename-args "sess-9" "")
                 '("session" "rename" "--session-id" "sess-9"
                   "--name" "" "--output" "json"))))

(ert-deftest safeslop-test-session-rename-empty-input-clears ()
  "`safeslop-session-rename' with empty input sends an empty --name (clear path)."
  (let (called)
    (cl-letf (((symbol-function 'safeslop--call-json-async)
               (lambda (args _cb) (setq called args))))
      (safeslop-session-rename "sess-1" "" nil t))
    (should (equal called
                   '("session" "rename" "--session-id" "sess-1"
                     "--name" "" "--output" "json")))))

(ert-deftest safeslop-test-portal-rename-key-and-run-detached-intact ()
  "N renames the session at point; R still runs detached (specs/0065 B1)."
  (should (eq (lookup-key safeslop-portal-mode-map (kbd "N")) #'safeslop-portal-rename))
  (should (eq (lookup-key safeslop-portal-mode-map (kbd "R")) #'safeslop-portal-run-detached)))

(ert-deftest safeslop-test-portal-rename-refreshes-in-place ()
  "Portal rename reads a name, calls rename with the id, and refreshes in place
without popping a result buffer over the dashboard."
  (let (called (refreshes 0) (shown 0))
    (cl-letf (((symbol-function 'safeslop-portal--session-at-point)
               (lambda () '((session_id . "sess-x") (name . "old"))))
              ((symbol-function 'read-string) (lambda (&rest _) "new label"))
              ((symbol-function 'safeslop--call-json-async)
               (lambda (args cb)
                 (setq called args)
                 (funcall cb (safeslop-contract-parse-string
                              "{\"schema_version\":1,\"ok\":true,\"data\":{\"session_id\":\"sess-x\",\"name\":\"new label\"},\"warnings\":[],\"errors\":[]}"))))
              ((symbol-function 'safeslop--show-envelope-buffer)
               (lambda (&rest _) (setq shown (1+ shown))))
              ((symbol-function 'safeslop-portal-refresh)
               (lambda () (setq refreshes (1+ refreshes)))))
      (safeslop-portal-rename))
    (should (equal called '("session" "rename" "--session-id" "sess-x"
                            "--name" "new label" "--output" "json")))
    (should (= refreshes 1))
    (should (= shown 0))))

(ert-deftest safeslop-test-session-detail-name-line ()
  "The detail view renders a Name: line only when the record has a name."
  (let ((with-name (safeslop-session--detail-format
                    '((session_id . "sess-1") (agent . "claude")
                      (name . "prod-fix") (status . "running") (workspace . "/w"))))
        (no-name (safeslop-session--detail-format
                  '((session_id . "sess-1") (agent . "claude")
                    (status . "running") (workspace . "/w")))))
    (should (string-match-p "Name:" with-name))
    (should (string-match-p "prod-fix" with-name))
    (should-not (string-match-p "Name:" no-name))))

(ert-deftest safeslop-test-portal-session-cell-shows-name ()
  "The Session cell suffixes the display name inline (specs/0065 N2) without
adding a Name column; a name-less row keeps the plain short id."
  (let* ((named '((session_id . "sess-abc") (agent . "claude")
                  (environment . "host") (network . "deny")
                  (status . "running") (workspace . "/w") (name . "prod")))
         (rows (safeslop-portal--rows (list named)))
         (cols (cadr (car rows))))
    ;; Session keeps the name inline (specs/0065 N2 adds no name column); the
    ;; 11th column now present is the new Creds column (specs/0086 T2).
    (should (= (length cols) 11))
    (should (string-match-p "prod" (aref cols 0)))
    (should (string-match-p "sess-abc" (aref cols 0))))
  (should (equal (safeslop-portal--session-cell '((session_id . "sess-xyz")))
                 (safeslop-portal--short-id "sess-xyz"))))

;;; Portal Creds column: value-free credential scope (specs/0086 T2) ----------

(ert-deftest safeslop-test-portal-credential-scope-string ()
  "One credential scope alist renders as a compact value-free \"kind name scope\".
Empty fields are dropped so a missing scope never leaves trailing whitespace."
  (should (equal (safeslop-portal--creds-scope-string
                  '((kind . "github") (name . "acme/web") (scope . "app rw")))
                 "github acme/web app rw"))
  (should (equal (safeslop-portal--creds-scope-string
                  '((kind . "pnpm") (name . "npm.pkg.github.com") (scope . "@org")))
                 "pnpm npm.pkg.github.com @org"))
  (should (equal (safeslop-portal--creds-scope-string
                  '((kind . "gcp") (name . "adc") (scope . "")))
                 "gcp adc")))

(ert-deftest safeslop-test-portal-credential-help-text ()
  "The full help/tooltip text comma-joins every scope; empty yields an honest note."
  (let ((sess '((credential_scopes . (((kind . "github") (name . "acme/web") (scope . "app rw"))
                                      ((kind . "pnpm") (name . "npm.pkg.github.com") (scope . "@org")))))))
    (should (equal (safeslop-portal--creds-help sess)
                   "github acme/web app rw, pnpm npm.pkg.github.com @org")))
  (should (equal (safeslop-portal--creds-help '((session_id . "sess-x")))
                 "no staged credentials")))

(ert-deftest safeslop-test-portal-credential-cell-compact ()
  "The Creds cell keeps +N visible inside the compact column width.
The full untruncated list rides help-echo."
  (let* ((sess '((credential_scopes . (((kind . "github") (name . "acme/web") (scope . "app rw"))
                                       ((kind . "pnpm") (name . "npm.pkg.github.com") (scope . "@org"))
                                       ((kind . "aws") (name . "dev") (scope . "us-east-1"))))))
         (cell (safeslop-portal--creds-cell sess))
         (text (substring-no-properties cell)))
    (should (string-prefix-p "github" text))
    (should (string-match-p "\\+2\\'" text))
    (should (<= (string-width text) safeslop-portal--creds-width))
    (should (equal (get-text-property 0 'help-echo cell)
                   "github acme/web app rw, pnpm npm.pkg.github.com @org, aws dev us-east-1"))))

(ert-deftest safeslop-test-portal-credential-cell-empty-em-dash ()
  "Ad-hoc / credential-less sessions (and old records) render the Creds cell as an em dash."
  (should (equal (safeslop-portal--creds-cell '((session_id . "sess-adhoc"))) "—"))
  (should (equal (safeslop-portal--creds-cell '((credential_scopes . ()))) "—"))
  (should (equal (safeslop-portal--creds-cell
                  '((credential_scopes . (((kind . "") (name . "") (scope . ""))))))
                 "—")))

(ert-deftest safeslop-test-portal-credential-old-json-renders-em-dash ()
  "An old session record with no credential_scopes field renders Creds as an em dash.
The field is additive and `--omitempty', so pre-0086 JSON simply lacks it (specs/0086)."
  (let* ((env (safeslop-contract-parse-string
               (concat "{\"schema_version\":1,\"ok\":true,\"data\":{\"sessions\":"
                       "[{\"session_id\":\"sess-old\",\"agent\":\"claude\",\"environment\":\"host\","
                       "\"network\":\"deny\",\"status\":\"running\",\"workspace\":\"/w\"}]},"
                       "\"warnings\":[],\"errors\":[]}")))
         (rows (safeslop-portal--rows (safeslop-portal--sessions-from env)))
         (cols (cadr (car rows))))
    (should (= (length cols) 11))
    (should (equal (aref cols 9) "—"))))          ; Creds column, empty for old records

(ert-deftest safeslop-test-portal-credential-row-has-creds-cell ()
  "`safeslop-portal--rows' places the compact Creds cell at index 9, before Workspace."
  (let* ((env (safeslop-contract-parse-string
               (concat "{\"schema_version\":1,\"ok\":true,\"data\":{\"sessions\":"
                       "[{\"session_id\":\"sess-c\",\"agent\":\"pi\",\"environment\":\"container\","
                       "\"network\":\"deny\",\"status\":\"running\",\"workspace\":\"/w\","
                       "\"credential_scopes\":[{\"kind\":\"github\",\"name\":\"acme/web\",\"scope\":\"app rw\"}]}]},"
                       "\"warnings\":[],\"errors\":[]}")))
         (rows (safeslop-portal--rows (safeslop-portal--sessions-from env)))
         (cols (cadr (car rows))))
    (should (= (length cols) 11))
    (should (string-prefix-p "github acme/web app rw"
                             (substring-no-properties (aref cols 9))))
    (should (equal (aref cols 10) "/w"))))       ; Workspace shifted one right

(ert-deftest safeslop-test-portal-credential-column-before-workspace ()
  "The portal defines a Creds column immediately before Workspace (specs/0086 T2)."
  (with-temp-buffer
    (safeslop-portal-mode)
    (let* ((titles (mapcar #'car (append tabulated-list-format nil)))
           (creds (cl-position "Creds" titles :test #'equal))
           (ws (cl-position "Workspace" titles :test #'equal)))
      (should creds)
      (should ws)
      (should (= (1+ creds) ws)))))

(ert-deftest safeslop-test-portal-credential-cell-value-free ()
  "The Creds cell/help render only safe kind/name/scope fields.
If a future JSON envelope regresses and carries token/ref/path-looking strings,
the Emacs portal still refuses to display them."
  (let* ((sess '((credential_scopes . (((kind . "github") (name . "acme/web") (scope . "app rw"))
                                       ((kind . "pnpm") (name . "op://vault/npm/token") (scope . "env:NPM_TOKEN"))
                                       ((kind . "ssh") (name . "/tmp/stage/key") (scope . "BEGIN PRIVATE KEY"))))))
         (cell (substring-no-properties (safeslop-portal--creds-cell sess)))
         (help (safeslop-portal--creds-help sess)))
    (dolist (leak '("op://" "env:" "token" "BEGIN" "PRIVATE KEY" "/tmp/stage"))
      (should-not (string-match-p (regexp-quote leak) cell))
      (should-not (string-match-p (regexp-quote leak) help)))
    (should (string-match-p "github acme/web app rw" help))
    (should (string-match-p "pnpm" help))
    (should (string-match-p "ssh" help))))

;;; Self-describing live buffers: name + header + id lookup (specs/0086 T3) ----

(ert-deftest safeslop-test-session-buffer-label-descriptive ()
  "A profile-backed session labels the buffer by profile, project, and tier/net,
matching the specs/0071 rec #3 example shape."
  (should (equal (safeslop-session--buffer-label
                  "sess-abc"
                  '((session_id . "sess-abc") (profile . "be-dev")
                    (workspace . "/home/me/payments")
                    (environment . "container") (network . "deny")))
                 "safeslop:be-dev payments [container/deny]")))

(ert-deftest safeslop-test-session-buffer-label-adhoc-and-name ()
  "No profile falls back to the display name; no profile/name uses the project."
  ;; name (no profile) becomes the identifier
  (should (equal (safeslop-session--buffer-label
                  "sess-1"
                  '((name . "prod-fix") (workspace . "/w/web")
                    (environment . "host") (network . "deny")))
                 "safeslop:prod-fix web [host/deny]"))
  ;; ad-hoc: neither profile nor name -> project + tier/net only
  (should (equal (safeslop-session--buffer-label
                  "sess-2"
                  '((workspace . "/w/api") (environment . "container") (network . "allow")))
                 "safeslop:api [container/allow]")))

(ert-deftest safeslop-test-session-buffer-label-fallback-old-id ()
  "With no legible data the label falls back to the legacy `safeslop-<id>' name,
so the buffer is still created and the portal legacy lookup still finds it."
  (should (equal (safeslop-session--buffer-label "sess-xyz" nil)
                 "safeslop-sess-xyz"))
  (should (equal (safeslop-session--buffer-label "sess-xyz" '((session_id . "sess-xyz")))
                 "safeslop-sess-xyz")))

(ert-deftest safeslop-test-session-buffer-label-value-free ()
  "The label embeds only the workspace basename and never a credential ref/value."
  (let ((label (safeslop-session--buffer-label
                "sess-1"
                '((profile . "be-dev") (workspace . "/home/secret/op-vault/payments")
                  (environment . "container") (network . "deny")
                  (credential_scopes . (((kind . "github") (name . "acme/web") (scope . "app rw"))))))))
    (should (equal label "safeslop:be-dev payments [container/deny]"))
    (should-not (string-match-p "/home/secret\\|op-vault\\|github\\|acme" label))))

(ert-deftest safeslop-test-session-buffer-label-sanitizes-unsafe-components ()
  "Unsafe profile/name/project text is dropped before building the label."
  (let ((label (safeslop-session--buffer-label
                "sess-safe"
                '((profile . "op://vault/profile")
                  (name . "env:NPM_TOKEN")
                  (workspace . "/w/env:NPM_TOKEN")
                  (environment . "container") (network . "deny")))))
    ;; With no safe profile/name/project left, do not render a bare tier-only
    ;; label; fall back to the legacy id form instead.
    (should (equal label "safeslop-sess-safe"))
    (dolist (leak '("op://" "env:" "TOKEN"))
      (should-not (string-match-p (regexp-quote leak) label))))
  (should (equal (safeslop-session--buffer-label
                  "sess-safe"
                  '((profile . "op://vault/profile") (name . "prod-fix")
                    (workspace . "/w/payments")
                    (environment . "container") (network . "deny")))
                 "safeslop:prod-fix payments [container/deny]")))

(ert-deftest safeslop-test-session-header-line-includes-creds ()
  "The header restates profile/project/tier/net and appends the value-free creds
list; the profile wins over the display name."
  (let ((header (safeslop-session--header-line
                 '((profile . "be-dev") (name . "ignored")
                   (workspace . "/srv/payments") (environment . "container")
                   (network . "deny")
                   (credential_scopes . (((kind . "github") (name . "acme/web") (scope . "app rw"))
                                         ((kind . "pnpm") (name . "npm.pkg.github.com") (scope . "@org"))))))))
    (should (string-match-p "be-dev" header))
    (should-not (string-match-p "ignored" header))
    (should (string-match-p "payments" header))
    (should (string-match-p "container/deny" header))
    (should (string-match-p "creds: github acme/web app rw, pnpm npm.pkg.github.com @org" header))))

(ert-deftest safeslop-test-session-header-line-old-record-safe ()
  "A pre-0086 record without credential_scopes (and empty/blank scope arrays)
produces a safe `creds: —' header (specs/0086 T3)."
  (let ((header (safeslop-session--header-line
                 '((profile . "legacy") (workspace . "/w/proj")
                   (environment . "host") (network . "deny")))))
    (should (string-match-p "creds: —" header))
    (should-not (string-match-p "op://\\|env:\\|token" header)))
  (should (string-match-p "creds: —"
                          (safeslop-session--header-line
                           '((profile . "p") (workspace . "/w/x")
                             (environment . "host") (network . "deny")
                             (credential_scopes . ())))))
  (should (string-match-p "creds: —"
                          (safeslop-session--header-line
                           '((profile . "p") (workspace . "/w/x")
                             (environment . "host") (network . "deny")
                             (credential_scopes . (((kind . "") (name . "") (scope . "")))))))))

(ert-deftest safeslop-test-session-header-line-value-free ()
  "The header renders only safe kind/name/scope; a regressed envelope carrying a
ref/value/staged path is still refused (mirrors the portal T2 guarantee)."
  (let* ((data '((profile . "be-dev") (workspace . "/home/me/payments")
                 (environment . "container") (network . "deny")
                 (credential_scopes . (((kind . "github") (name . "acme/web") (scope . "app rw"))
                                       ((kind . "pnpm") (name . "op://vault/npm/token") (scope . "env:NPM_TOKEN"))
                                       ((kind . "ssh") (name . "/tmp/stage/key") (scope . "BEGIN PRIVATE KEY"))))))
         (header (safeslop-session--header-line data)))
    (dolist (leak '("op://" "env:" "token" "BEGIN" "PRIVATE KEY" "/tmp/stage" "/home/me"))
      (should-not (string-match-p (regexp-quote leak) header)))
    (should (string-match-p "github acme/web app rw" header))
    (should (string-match-p "pnpm" header))
    (should (string-match-p "ssh" header))))

(ert-deftest safeslop-test-session-header-line-sanitizes-label-fields ()
  "The header must not leak unsafe profile/name/project label components."
  (let ((header (safeslop-session--header-line
                 '((session_id . "sess-safe")
                   (profile . "op://vault/profile")
                   (name . "env:NPM_TOKEN")
                   (workspace . "/w/env:NPM_TOKEN")
                   (environment . "container") (network . "deny")))))
    (should (string-prefix-p "safeslop-sess-safe" header))
    (should (string-match-p "creds: —" header))
    (dolist (leak '("op://" "env:" "TOKEN"))
      (should-not (string-match-p (regexp-quote leak) header)))))

(ert-deftest safeslop-test-session-launch-term-sets-buffer-local-id-and-header ()
  "launch-term fetches data best-effort, names the buffer descriptively, sets the
buffer-local id after terminal creation, and installs a value-free creds header."
  (let ((made-name nil) (buf nil) (preflighted nil))
    (unwind-protect
        (cl-letf (((symbol-function 'safeslop-session--fetch-data)
                   (lambda (_id)
                     '((session_id . "sess-xyz") (profile . "be-dev")
                       (workspace . "/home/me/payments")
                       (environment . "container") (network . "deny")
                       (credential_scopes . (((kind . "github") (name . "acme/web") (scope . "app rw")))))))
                  ((symbol-function 'safeslop-session--run-runtime-preflight)
                   (lambda (data) (setq preflighted data) data))
                  ((symbol-function 'safeslop-session--make-terminal)
                   (lambda (name _program _argv)
                     (setq made-name name)
                     (setq buf (get-buffer-create (concat "*" name "*")))))
                  ((symbol-function 'pop-to-buffer) (lambda (b &rest _) b)))
          (safeslop-session--launch-term "sess-xyz" '("session" "run" "--session-id" "sess-xyz"))
          (should (equal (alist-get 'session_id preflighted) "sess-xyz"))
          (should (equal made-name "safeslop:be-dev payments [container/deny]"))
          (with-current-buffer buf
            (should (equal safeslop-session-id "sess-xyz"))
            (should (stringp header-line-format))
            (should (string-match-p "creds: github acme/web app rw" header-line-format))
            (should-not (string-match-p "op://\\|env:\\|/home/me" header-line-format))))
      (when (buffer-live-p buf) (kill-buffer buf)))))

(ert-deftest safeslop-test-session-launch-term-fallback-legacy-buffer-name ()
  "A best-effort fetch miss must not block the launch: the buffer keeps the
legacy `safeslop-<id>' name and installs no header."
  (let ((made-name nil) (buf nil))
    (unwind-protect
        (cl-letf (((symbol-function 'safeslop-session--fetch-data) (lambda (_id) nil))
                  ((symbol-function 'safeslop-session--make-terminal)
                   (lambda (name _program _argv)
                     (setq made-name name)
                     (setq buf (get-buffer-create (concat "*" name "*")))))
                  ((symbol-function 'pop-to-buffer) (lambda (b &rest _) b)))
          (safeslop-session--launch-term "sess-none" '("session" "run"))
          (should (equal made-name "safeslop-sess-none"))
          (with-current-buffer buf
            (should (equal safeslop-session-id "sess-none"))
            (should (null header-line-format))))
      (when (buffer-live-p buf) (kill-buffer buf)))))

(ert-deftest safeslop-test-session-live-buffer-found-by-id ()
  "The portal live lookup scans for the buffer-local session id, so a buffer
renamed to its descriptive form is still found (specs/0086 T3)."
  (let ((buf (get-buffer-create "*safeslop:be-dev payments [container/deny]*")))
    (unwind-protect
        (progn
          (with-current-buffer buf (setq-local safeslop-session-id "sess-live"))
          (should (eq (safeslop-portal--live-buffer "sess-live") buf))
          (should-not (safeslop-portal--live-buffer "sess-missing")))
      (kill-buffer buf))))

(ert-deftest safeslop-test-session-live-buffer-legacy-fallback ()
  "A legacy buffer named `*safeslop-<id>*' without a buffer-local id is still
found by name, so pre-0086 terminals keep working."
  (let ((buf (get-buffer-create "*safeslop-sess-old*")))
    (unwind-protect
        (should (eq (safeslop-portal--live-buffer "sess-old") buf))
      (kill-buffer buf))))

(ert-deftest safeslop-test-session-live-open-pops-renamed-buffer ()
  "Portal `open' on a running coupled session pops to the id-tagged buffer even
after its name became descriptive (specs/0086 T3)."
  (let ((buf (get-buffer-create "*safeslop:be-dev payments [container/deny]*"))
        (popped nil))
    (unwind-protect
        (cl-letf (((symbol-function 'safeslop-portal--session-at-point)
                   (lambda () '((session_id . "sess-live") (status . "running") (socket . ""))))
                  ((symbol-function 'pop-to-buffer) (lambda (b &rest _) (setq popped b))))
          (with-current-buffer buf (setq-local safeslop-session-id "sess-live"))
          (safeslop-portal-open)
          (should (eq popped buf)))
      (kill-buffer buf))))
