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
  (let (initial-state)
    (cl-letf (((symbol-function 'evil-set-initial-state)
               (lambda (mode state) (setq initial-state (list mode state))))
              ((symbol-function 'evil-define-key)
               (lambda (_state keymap key def &rest bindings)
                 (define-key keymap key def)
                 (while bindings
                   (define-key keymap (pop bindings) (pop bindings))))))
      (unless (featurep 'evil)
        (provide 'evil))
      (should (equal initial-state '(safeslop-output-mode normal)))
      (should (eq (lookup-key safeslop-output-mode-map (kbd "g")) #'safeslop-doctor))
      (should (eq (lookup-key safeslop-output-mode-map (kbd "q")) #'quit-window)))))

;;; safeslop-test.el ends here
