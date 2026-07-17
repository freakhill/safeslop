;;; safeslop-egress.el --- Progressive session egress UI -*- lexical-binding: t; -*-

;; Copyright (C) 2026

;; Author: safeslop
;; Package-Requires: ((emacs "32.0"))
;; Keywords: tools, processes, ai

;;; Commentary:

;; Explicit session-scoped egress command construction and passive review UI.
;; `safeslop-session.el' remains the public session front.

;;; Code:

(require 'subr-x)
(require 'cl-lib)
(require 'safeslop-contract)
(require 'safeslop-client)
(require 'safeslop-surface)
(require 'safeslop-output)
(require 'safeslop-session-terminal)

(declare-function safeslop-session-egress-grant "safeslop-session"
                  (&optional session-id host port callback quiet))
(declare-function safeslop-session-egress-dismiss "safeslop-session"
                  (&optional session-id host port callback quiet))
(declare-function safeslop-session-egress-observations "safeslop-session"
                  (&optional session-id callback quiet))
(declare-function safeslop-session-egress-review "safeslop-session"
                  (&optional session-id session-data))

;; specs/0097: Egress observation is strictly read-only.  The three mutation
;; functions below are explicit operator commands; no agent/proxy event calls
;; them, and none edits safeslop.cue or profile egress policy.
(defun safeslop-session--egress-observations-args (session-id)
  "Return exact argv for SESSION-ID's value-free denied observations."
  (list "session" "egress" "observations" "--session-id" session-id "--output" "json"))

(defun safeslop-session--egress-grants-args (session-id)
  "Return exact argv for SESSION-ID's active session-scoped grants."
  (list "session" "egress" "grants" "--session-id" session-id "--output" "json"))

(defun safeslop-session--egress-grant-args (session-id host port)
  "Return exact argv to grant HOST:PORT for SESSION-ID."
  (list "session" "egress" "grant" "--session-id" session-id "--host" host
        "--port" (number-to-string port) "--output" "json"))

(defun safeslop-session--egress-revoke-args (session-id grant-id)
  "Return exact argv to revoke GRANT-ID from SESSION-ID."
  (list "session" "egress" "revoke" "--session-id" session-id "--grant-id" grant-id
        "--output" "json"))

(defun safeslop-session--egress-dismiss-args (session-id host port)
  "Return exact argv to keep HOST:PORT denied for SESSION-ID."
  (list "session" "egress" "dismiss" "--session-id" session-id "--host" host
        "--port" (number-to-string port) "--output" "json"))

(defun safeslop-session--profile-egress-args (operation profile policy-path host port policy-hash)
  "Return exact argv for explicit durable OPERATION on PROFILE's typed rule.
POLICY-HASH is always supplied from the session snapshot so a changed policy
fails closed instead of silently editing newer reviewed bytes."
  (append (list "profile" "egress" operation profile)
          (when (and (stringp policy-path) (not (string-prefix-p "builtin:" policy-path)))
            (list policy-path))
          (list "--host" host "--port" (number-to-string port)
                "--expected-policy-hash" policy-hash "--output" "json")))

(defun safeslop-session--egress-dispatch (args buffer-name callback quiet)
  "Dispatch egress ARGS asynchronously, rendering BUFFER-NAME unless QUIET.
CALLBACK receives the JSON envelope.  This is deliberately a thin explicit CLI
bridge: it never consults or writes a profile policy."
  (safeslop--call-json-async
   args
   (lambda (envelope)
     (unless quiet
       (safeslop--show-envelope-buffer buffer-name args envelope))
     (when callback (funcall callback envelope)))))

(defun safeslop-session--review-observation-at-point ()
  "Return the value-free review observation at point, or signal clearly."
  (or (get-text-property (point) 'safeslop-egress-observation)
      (user-error "Move point to a denied destination")))

(defun safeslop-session--open-review-buffer (name title loading)
  "Open operator-requested NAME once, then return it for an async update.
LOADING is rendered before dispatch so a later proxy response cannot
focus or pop any window."
  (let ((buf (get-buffer-create name)))
    (with-current-buffer buf
      (safeslop-output-mode)
      ;; Review keys carry session-specific closures; isolate them from the
      ;; shared output-mode map used by unrelated read-only surfaces.
      (use-local-map (copy-keymap (current-local-map)))
      (let ((inhibit-read-only t))
        (erase-buffer)
        (insert title "\n" loading "\n")))
    (pop-to-buffer buf)
    buf))

(defun safeslop-session--review-render (session-id session-data envelope &optional buffer)
  "Render a value-free operator review into BUFFER without selecting it."
  (let ((buf (or buffer (get-buffer-create "*safeslop egress review*")))
        (observations (alist-get 'observations (safeslop-contract-data envelope))))
    (when (buffer-live-p buf)
      (with-current-buffer buf
        (let ((inhibit-read-only t))
          (erase-buffer)
          (insert (format "Progressive egress review — session %s\n" session-id))
          (insert "Passive observations are denied traffic, not prompts or authority.\n")
          (insert "Keys: a Allow now, k Keep denied, A Always allow, g refresh, q quit\n\n")
          (if (not (safeslop-contract-ok-p envelope))
              (insert "Could not read observations; retry with g.\n")
            (if (null observations)
                (insert "No pending denied destinations.\n")
              (dolist (obs observations)
                (let ((start (point))
                      (host (safeslop-session--safe-display-field (alist-get 'host obs)))
                      (port (alist-get 'port obs)))
                  ;; Deliberately render no request URI, header, or payload.
                  (insert (format "%s:%s  count=%s  last=%s  %s\n"
                                  (or host "[redacted]") (or port "?")
                                  (or (alist-get 'count obs) 0)
                                  (or (alist-get 'last_seen obs) "?")
                                  (if (eq (alist-get 'grantable obs) t) "grantable" "keep denied")))
                  (put-text-property start (point) 'safeslop-egress-observation obs)))))
          (local-set-key (kbd "a")
                         (lambda () (interactive)
                           (let ((o (safeslop-session--review-observation-at-point)))
                             (safeslop-session-egress-grant session-id (alist-get 'host o) (alist-get 'port o) nil t))))
          (local-set-key (kbd "k")
                         (lambda () (interactive)
                           (let ((o (safeslop-session--review-observation-at-point)))
                             (safeslop-session-egress-dismiss session-id (alist-get 'host o) (alist-get 'port o) nil t))))
          (local-set-key (kbd "A")
                         (lambda () (interactive)
                           (safeslop-session--profile-egress-review
                            session-data (safeslop-session--review-observation-at-point))))
          (local-set-key (kbd "g") (lambda () (interactive) (safeslop-session-egress-review session-id session-data)))
          (goto-char (point-min)))))))

(defun safeslop-session--profile-egress-render (session-data observation envelope buffer)
  "Render the hash-checked persistent-rule review into BUFFER, never focusing it."
  (when (buffer-live-p buffer)
    (with-current-buffer buffer
      (let ((inhibit-read-only t)
            (data (safeslop-contract-data envelope)))
        (erase-buffer)
        (if (not (safeslop-contract-ok-p envelope))
            (progn
              ;; A stale snapshot is a hard stop: add is deliberately unbound.
              (insert "Policy changed; no persistent rule was written. Re-open review to inspect current policy.\n")
              (local-set-key (kbd "a") nil))
          (insert "Persistent egress review — future sessions only\n")
          (insert (format "Profile: %s\n" (or (alist-get 'profile data) "[redacted]")))
          (insert (format "Current policy hash: %s\n" (or (alist-get 'current_policy_hash data) "[redacted]")))
          (insert (format "Candidate policy hash: %s\n" (or (alist-get 'candidate_policy_hash data) "[redacted]")))
          (insert (format "Delta: + persistentEgress: {%s, %s}\n"
                          (or (safeslop-session--safe-display-field (alist-get 'host observation)) "[redacted]")
                          (or (alist-get 'port observation) "?")))
          (insert "Source/lifetime: profile-persistent / future sessions\n")
          (insert "\nPress a to add this exact rule, changing policy bytes; then review and re-trust before a new session can use it.\n")
          (local-set-key
           (kbd "a")
           (lambda () (interactive)
             (safeslop-session--egress-dispatch
              (safeslop-session--profile-egress-args
               "add" (alist-get 'profile session-data) (alist-get 'policy_path session-data)
               (alist-get 'host observation) (alist-get 'port observation)
               (alist-get 'policy_hash session-data))
              "*safeslop profile egress add*"
              (lambda (result)
                (if (safeslop-contract-ok-p result)
                    (message "safeslop: persistent rule written; review and re-trust before creating a new session")
                  (safeslop-session--profile-egress-render session-data observation result buffer)))
              t))))
        (goto-char (point-min))))))

(defun safeslop-session--profile-egress-review (session-data observation)
  "Preview OBSERVATION as a durable rule; only a later explicit key writes it."
  (let ((profile (alist-get 'profile session-data))
        (hash (alist-get 'policy_hash session-data))
        (path (alist-get 'policy_path session-data)))
    (unless (and (stringp profile) (stringp hash)
                 (not (string-prefix-p "builtin:" (or path ""))))
      (user-error "Always allow requires a project profile snapshot"))
    (let ((buf (safeslop-session--open-review-buffer
                "*safeslop profile egress review*" "Persistent egress review"
                "Loading hash-checked policy delta; no policy is being changed.")))
      (safeslop-session--egress-dispatch
       (safeslop-session--profile-egress-args "preview" profile path
                                             (alist-get 'host observation) (alist-get 'port observation) hash)
       "*safeslop profile egress preview*"
       (lambda (envelope) (safeslop-session--profile-egress-render session-data observation envelope buf)) t))))

(defun safeslop-session--egress-grants-summary (data)
  "Return DATA's value-free session grants as a compact detail string."
  (let ((grants (alist-get 'egress_grants data))
        (revision (or (alist-get 'egress_grant_revision data) 0)))
    (if (null grants)
        (format "none (revision %s)" revision)
      (format "%s (revision %s)"
              (mapconcat
               (lambda (grant)
                 (format "%s:%s (%s)"
                         (or (alist-get 'host grant) "?")
                         (or (alist-get 'port grant) "?")
                         (or (alist-get 'id grant) "?")))
               grants ", ")
              revision))))


(defun safeslop-session--detail-pending-render (buffer envelope)
  "Replace BUFFER's passive egress-count line without selecting its window."
  (when (buffer-live-p buffer)
    (with-current-buffer buffer
      (let ((inhibit-read-only t)
            (count (alist-get 'pending_count (safeslop-contract-data envelope))))
        (save-excursion
          (goto-char (point-min))
          (when (re-search-forward "^Egress review:.*$" nil t)
            (replace-match
             (if (and (safeslop-contract-ok-p envelope) (integerp count))
                 (format "Egress review: %d pending denied destination%s (v to review)"
                         count (if (= count 1) "" "s"))
               "Egress review: unavailable; press v to retry"))))))))

(defun safeslop-session--detail-request-pending-count (session-id data buffer)
  "Asynchronously discover the passive count for a container-deny detail view."
  (when (and (equal (alist-get 'environment data) "container")
             (equal (alist-get 'network data) "deny"))
    (safeslop-session-egress-observations
     session-id
     (lambda (envelope) (safeslop-session--detail-pending-render buffer envelope)) t)))

(provide 'safeslop-egress)
;;; safeslop-egress.el ends here
