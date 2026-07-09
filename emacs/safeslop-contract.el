;;; safeslop-contract.el --- JSON contract parser for safeslop -*- lexical-binding: t; -*-

;; Copyright (C) 2026

;; Author: safeslop
;; Package-Requires: ((emacs "32.0"))
;; Keywords: tools, json

;;; Commentary:

;; Parser/validator for the versioned safeslop JSON envelope shared with Go's
;; internal/jsoncontract package.  Tests parse the Go golden fixtures directly.

;;; Code:

(require 'cl-lib)
(require 'json)
(require 'subr-x)

(defconst safeslop-contract-schema-version 1
  "Current safeslop JSON contract schema version.")

(defconst safeslop-contract-error-codes
  '("INVALID_ARGUMENT"
    "SCHEMA_UNSUPPORTED"
    "SCHEMA_VIOLATION"
    "NOT_FOUND"
    "CONFLICT"
    "PERMISSION_DENIED"
    "AUTH_REQUIRED"
    "CREDENTIAL_REVOKED"
    "CREDENTIAL_REVOKE_FAILED"
    "POLICY_DENIED"
    "NETWORK_DENIED"
    "SANDBOX_DENIED"
    "SANDBOX_UNAVAILABLE"
    "RUNTIME_UNAVAILABLE"
    "TOOL_UNAVAILABLE"
    "AGENT_UNSUPPORTED"
    "SESSION_NOT_FOUND"
    "SESSION_ALREADY_RUNNING"
    "SESSION_NOT_RUNNING"
    "SESSION_STOPPED"
    "SESSION_CANCELLED"
    "PTY_UNAVAILABLE"
    "TIMEOUT"
    "RATE_LIMITED"
    "IO_ERROR"
    "INTERNAL"
    "TRUST_REQUIRED")
  "Append-only v1 safeslop JSON contract error-code registry.")

(define-error 'safeslop-contract-error "safeslop JSON contract error")

(defun safeslop-contract--bool (value)
  "Return non-nil when VALUE is the JSON boolean true."
  (eq value t))

(defun safeslop-contract--alist-p (value)
  "Return non-nil when VALUE is a JSON object represented as an alist."
  (or (null value)
      (and (listp value)
           (cl-every (lambda (entry) (and (consp entry) (symbolp (car entry)))) value))))

(defun safeslop-contract--message-valid-p (message)
  "Return non-nil when MESSAGE has the v1 warning/error shape."
  (and (safeslop-contract--alist-p message)
       (let ((code (alist-get 'code message))
             (text (alist-get 'message message))
             (details (alist-get 'details message))
             (retryable (alist-get 'retryable message)))
         (and (stringp code)
              (member code safeslop-contract-error-codes)
              (stringp text)
              (not (string-empty-p text))
              (safeslop-contract--alist-p details)
              (memq retryable '(t :json-false))))))

(defun safeslop-contract-validate (envelope)
  "Validate parsed ENVELOPE and return it, or signal `safeslop-contract-error'."
  (unless (safeslop-contract--alist-p envelope)
    (signal 'safeslop-contract-error '("envelope must be a JSON object")))
  (let ((schema-version (alist-get 'schema_version envelope))
        (ok (alist-get 'ok envelope))
        (data (alist-get 'data envelope))
        (warnings (alist-get 'warnings envelope))
        (errors (alist-get 'errors envelope)))
    (unless (and (integerp schema-version)
                 (= schema-version safeslop-contract-schema-version))
      (signal 'safeslop-contract-error
              (list (format "unsupported schema_version %S" schema-version))))
    (unless (memq ok '(t :json-false))
      (signal 'safeslop-contract-error '("ok must be a boolean")))
    (unless (safeslop-contract--alist-p data)
      (signal 'safeslop-contract-error '("data must be an object")))
    (unless (listp warnings)
      (signal 'safeslop-contract-error '("warnings must be an array")))
    (unless (listp errors)
      (signal 'safeslop-contract-error '("errors must be an array")))
    (dolist (warning warnings)
      (unless (safeslop-contract--message-valid-p warning)
        (signal 'safeslop-contract-error '("invalid warning message"))))
    (dolist (err errors)
      (unless (safeslop-contract--message-valid-p err)
        (signal 'safeslop-contract-error '("invalid error message"))))
    (when (and (safeslop-contract--bool ok) errors)
      (signal 'safeslop-contract-error '("ok envelope must not include errors")))
    (when (and (not (safeslop-contract--bool ok)) (null errors))
      (signal 'safeslop-contract-error '("error envelope must include at least one error")))
    envelope))

(defun safeslop-contract-parse-string (json-string)
  "Parse JSON-STRING as a validated safeslop contract envelope."
  (safeslop-contract-validate
   (json-parse-string json-string
                      :object-type 'alist
                      :array-type 'list
                      :false-object :json-false
                      :null-object :json-null)))

(defun safeslop-contract-parse-file (file)
  "Parse FILE as a validated safeslop contract envelope."
  (with-temp-buffer
    (insert-file-contents file)
    (safeslop-contract-parse-string (buffer-string))))

(defun safeslop-contract-ok-p (envelope)
  "Return non-nil when ENVELOPE is successful."
  (safeslop-contract--bool (alist-get 'ok envelope)))

(defun safeslop-contract-data (envelope)
  "Return ENVELOPE data object."
  (alist-get 'data envelope))

(defun safeslop-contract-warnings (envelope)
  "Return ENVELOPE warnings list."
  (alist-get 'warnings envelope))

(defun safeslop-contract-errors (envelope)
  "Return ENVELOPE errors list."
  (alist-get 'errors envelope))

(defun safeslop-contract-first-error-code (envelope)
  "Return the first error code in ENVELOPE, or nil."
  (alist-get 'code (car (safeslop-contract-errors envelope))))

(defconst safeslop-contract-creds-status-probes
  '("ok" "secret-unresolved" "unreachable" "denied" "error")
  "Value-free probe classes returned by `creds status --output json'.")

(defconst safeslop-contract-creds-status--forbidden-link-keys
  '(privateKeyRef tokenRef keyRef ref token value stagedPath path)
  "Secret-bearing or stage-path fields forbidden in account-link status rows.")

(defun safeslop-contract--nonempty-string-p (value)
  "Return non-nil when VALUE is a non-empty string."
  (and (stringp value) (not (string-empty-p value))))

(defun safeslop-contract--optional-int-p (value)
  "Return non-nil when VALUE is absent/null or an integer."
  (or (null value) (eq value :json-null) (integerp value)))

(defun safeslop-contract--creds-status-link-valid-p (link)
  "Return non-nil when LINK has the value-free account-status row shape."
  (and (safeslop-contract--alist-p link)
       (not (cl-some (lambda (key) (assq key link))
                     safeslop-contract-creds-status--forbidden-link-keys))
       (let ((forge (alist-get 'forge link))
             (host (alist-get 'host link))
             (owner (alist-get 'owner link))
             (probe (alist-get 'probe link))
             (ttl (alist-get 'ttl link))
             (app-id (alist-get 'appID link))
             (installation-id (alist-get 'installationID link))
             (ssh-port (alist-get 'sshPort link)))
         (and (member forge '("github" "forgejo"))
              (safeslop-contract--nonempty-string-p host)
              (safeslop-contract--nonempty-string-p owner)
              (member probe safeslop-contract-creds-status-probes)
              (safeslop-contract--nonempty-string-p ttl)
              (safeslop-contract--optional-int-p app-id)
              (safeslop-contract--optional-int-p installation-id)
              (safeslop-contract--optional-int-p ssh-port)))))

(defun safeslop-contract-creds-status-links (envelope)
  "Return validated `data.links' from a `creds status --output json' ENVELOPE.
Rows are value-free account-link status records: forge, host, owner,
non-secret ids, probe class, SSH port, and TTL model only.  Secret refs/values
and staged paths are rejected rather than silently ignored."
  (safeslop-contract-validate envelope)
  (unless (safeslop-contract-ok-p envelope)
    (signal 'safeslop-contract-error '("creds status envelope must be ok")))
  (let* ((data (safeslop-contract-data envelope))
         (links (alist-get 'links data)))
    (unless (listp links)
      (signal 'safeslop-contract-error '("data.links must be an array")))
    (dolist (link links)
      (unless (safeslop-contract--creds-status-link-valid-p link)
        (signal 'safeslop-contract-error '("invalid creds status link"))))
    links))

(provide 'safeslop-contract)
;;; safeslop-contract.el ends here
