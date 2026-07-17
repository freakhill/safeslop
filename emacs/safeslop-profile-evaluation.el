;;; safeslop-profile-evaluation.el --- Profile safety evaluation -*- lexical-binding: t; -*-

;; Copyright (C) 2026

;; Author: safeslop
;; Package-Requires: ((emacs "32.0"))
;; Keywords: tools, processes, ai

;;; Commentary:

;; Validation and rendering for the versioned profile safety evaluation.
;; `safeslop-profiles.el' remains the public profile front.

;;; Code:

(require 'subr-x)
(require 'cl-lib)
(require 'button)
(require 'safeslop-surface)

;; Typed remediation dispatch lives in the profile front (`safeslop-profiles.el'),
;; the layer that legitimately orchestrates sibling surfaces such as credentials.
;; This shard only builds the value-free button metadata and calls dispatch
;; late-bound, so the evaluation layer never reaches upward into credentials at
;; load time (dependency inversion; keeps the strict layering in the module map).
(declare-function safeslop-profiles--dispatch-remediation "safeslop-profiles"
                  (kind action-id docs-ref))

;;; ---- structured profile evaluation -------------------------------------

(defface safeslop-profile-evaluation-concern
  '((t :inherit error :weight bold))
  "Face reinforcing an explicit CONCERN evaluation outcome."
  :group 'safeslop)

(defface safeslop-profile-evaluation-bounded
  '((t :inherit warning :weight bold))
  "Face reinforcing an explicit BOUNDED evaluation outcome."
  :group 'safeslop)

(defface safeslop-profile-evaluation-pass
  '((t :inherit success :weight bold))
  "Face reinforcing an explicit PASS evaluation outcome."
  :group 'safeslop)

(defface safeslop-profile-evaluation-fail
  '((t :inherit error :weight bold))
  "Face reinforcing an explicit FAIL evaluation outcome."
  :group 'safeslop)

(defface safeslop-profile-evaluation-unknown
  '((t :inherit warning :weight bold))
  "Face reinforcing an explicit UNKNOWN outcome; deliberately not green."
  :group 'safeslop)

(defface safeslop-profile-evaluation-not-applicable
  '((t :inherit shadow :weight bold))
  "Face reinforcing an explicit N/A outcome; deliberately not green."
  :group 'safeslop)

(defconst safeslop-profiles--evaluation-axes
  '("network" "files" "projection" "secrets" "credentials" "trust" "readiness")
  "Closed v1 profile-evaluation finding axes.")

(defconst safeslop-profiles--evaluation-authority-axes
  '("network" "files" "projection" "secrets" "credentials")
  "Required v1 authority axes, in engine order.")

(defconst safeslop-profiles--evaluation-outcomes
  '("concern" "bounded" "pass" "fail" "unknown" "not_applicable")
  "Closed v1 profile-evaluation finding outcomes.")

(defconst safeslop-profiles--evaluation-severities
  '("critical" "high" "medium" "info")
  "Closed v1 profile-evaluation finding severities.")

(defconst safeslop-profiles--evaluation-remediation-kinds
  '("policy_change" "operator_workflow" "install_helper"
    "repair_helper_resolution" "review_and_trust" "retry_check")
  "Closed v1 typed remediation kinds.")

(defconst safeslop-profiles--evaluation-trust-states
  '("trusted" "untrusted" "changed" "unknown" "not_applicable")
  "Closed v1 trust states.")

(defconst safeslop-profiles--evaluation-trust-bases
  '("project_exact_bytes" "embedded_builtin" "unsaved" "unknown")
  "Closed v1 trust bases.")

(defconst safeslop-profiles--evaluation-readiness-states
  '("ready" "blocked" "unknown" "not_applicable")
  "Closed v1 readiness states.")

(defconst safeslop-profiles--evaluation-credential-providers
  '("pnpm" "aws" "gcp" "kube" "github" "forgejo")
  "Closed v1 credential-scope providers.")

(defconst safeslop-profiles--evaluation-credential-access
  '("read_only" "read_write" "scoped_api" "external_policy"
    "provider_default" "unknown")
  "Closed v1 credential-scope access values.")

(defconst safeslop-profiles--evaluation-credential-lifetimes
  '("short_lived" "persistent" "unknown")
  "Closed v1 credential-scope lifetime values.")

(defconst safeslop-profiles--evaluation-credential-bases
  '("declared" "resolved_at_launch" "provider_default")
  "Closed v1 credential-scope basis values.")

(defconst safeslop-profiles--evaluation-stable-id-regexp
  "\\`[a-z0-9]+\\(?:[.-][a-z0-9]+\\)*\\'"
  "Regexp for engine-owned rule symbols.")

(defconst safeslop-profiles--evaluation-action-id-regexp
  "\\`[a-z0-9]+\\(?:-[a-z0-9]+\\)*\\'"
  "Regexp for typed v1 remediation action IDs.")

(defconst safeslop-profiles--evaluation-rfc3339-regexp
  (concat "\\`[0-9]\\{4\\}-[0-9]\\{2\\}-[0-9]\\{2\\}T"
          "[0-9]\\{2\\}:[0-9]\\{2\\}:[0-9]\\{2\\}"
          "\\(?:\\.[0-9]+\\)?\\(?:Z\\|[+-][0-9]\\{2\\}:[0-9]\\{2\\}\\)\\'")
  "Wire timestamp shape for a v1 trust/readiness snapshot.")

(defun safeslop-profiles--evaluation-object-p (value)
  "Return non-nil when VALUE has the JSON-object alist representation."
  (and (listp value)
       (cl-every (lambda (entry) (and (consp entry) (symbolp (car entry)))) value)))

(defun safeslop-profiles--evaluation-required-error (object fields label)
  "Return a validation error when OBJECT lacks any required FIELDS for LABEL."
  (cond
   ((not (safeslop-profiles--evaluation-object-p object))
    (format "%s must be an object" label))
   ((cl-find-if-not (lambda (field) (assq field object)) fields)
    (format "%s is missing required fields" label))))

(defun safeslop-profiles--evaluation-nonempty-string-p (value)
  "Return non-nil when VALUE is a non-empty string."
  (and (stringp value) (not (string-empty-p value))))

(defun safeslop-profiles--evaluation-stable-id-p (value)
  "Return non-nil when VALUE is a lowercase engine-owned stable symbol."
  (and (safeslop-profiles--evaluation-nonempty-string-p value)
       (string-match-p safeslop-profiles--evaluation-stable-id-regexp value)))

(defun safeslop-profiles--evaluation-timestamp-p (value)
  "Return non-nil when VALUE has the v1 UTC/offset RFC3339 wire shape."
  (and (stringp value)
       (string-match-p safeslop-profiles--evaluation-rfc3339-regexp value)))

(defun safeslop-profiles--evaluation-remediation-error (remediation)
  "Return nil for valid typed REMEDIATION, otherwise a v1 validation error."
  (or
   (safeslop-profiles--evaluation-required-error
    remediation '(kind action_id summary docs_ref) "remediation")
   (let ((kind (alist-get 'kind remediation))
         (action-id (alist-get 'action_id remediation))
         (summary (alist-get 'summary remediation))
         (docs-ref (alist-get 'docs_ref remediation)))
     (cond
      ((not (member kind safeslop-profiles--evaluation-remediation-kinds))
       "remediation kind is unsupported")
      ((or (not (safeslop-profiles--evaluation-nonempty-string-p action-id))
           (not (string-match-p safeslop-profiles--evaluation-action-id-regexp
                                action-id)))
       "remediation action_id is malformed")
      ((not (safeslop-profiles--evaluation-nonempty-string-p summary))
       "remediation summary is missing")
      ((not (safeslop-profiles--evaluation-nonempty-string-p docs-ref))
       "remediation docs_ref is missing")))))

(defun safeslop-profiles--evaluation-credential-scope-error (scope)
  "Return nil for a structurally valid value-free credential SCOPE."
  (or
   (safeslop-profiles--evaluation-required-error
    scope '(scope_id provider target access lifetime basis) "credential scope")
   (let ((scope-id (alist-get 'scope_id scope))
         (provider (alist-get 'provider scope))
         (target (alist-get 'target scope))
         (access (alist-get 'access scope))
         (lifetime (alist-get 'lifetime scope))
         (basis (alist-get 'basis scope)))
     (cond
      ((not (member provider safeslop-profiles--evaluation-credential-providers))
       "credential provider is unsupported")
      ((or (not (stringp scope-id))
           (not (string-match-p
                 (format "\\`credential\\.%s\\.[0-9]\\{3\\}\\'"
                         (regexp-quote provider))
                 scope-id)))
       "credential scope_id is malformed or does not match provider")
      ((not (safeslop-profiles--evaluation-nonempty-string-p target))
       "credential target is missing")
      ((not (member access safeslop-profiles--evaluation-credential-access))
       "credential access is unsupported")
      ((not (member lifetime safeslop-profiles--evaluation-credential-lifetimes))
       "credential lifetime is unsupported")
      ((not (member basis safeslop-profiles--evaluation-credential-bases))
       "credential basis is unsupported")))))

(defun safeslop-profiles--evaluation-finding-error (finding allowed-axes scope-ids)
  "Return nil for a valid FINDING in ALLOWED-AXES referencing SCOPE-IDS."
  (or
   (safeslop-profiles--evaluation-required-error
    finding '(rule_id axis outcome severity title consequence scope_ids remediation) "finding")
   (let ((rule-id (alist-get 'rule_id finding))
         (axis (alist-get 'axis finding))
         (outcome (alist-get 'outcome finding))
         (severity (alist-get 'severity finding))
         (title (alist-get 'title finding))
         (consequence (alist-get 'consequence finding))
         (finding-scopes (alist-get 'scope_ids finding))
         (remediation (alist-get 'remediation finding)))
     (cond
      ((not (safeslop-profiles--evaluation-stable-id-p rule-id))
       "finding rule_id is malformed")
      ((or (not (member axis safeslop-profiles--evaluation-axes))
           (not (member axis allowed-axes)))
       "finding axis is unsupported in its section")
      ((not (member outcome safeslop-profiles--evaluation-outcomes))
       "finding outcome is unsupported")
      ((not (member severity safeslop-profiles--evaluation-severities))
       "finding severity is unsupported")
      ((not (safeslop-profiles--evaluation-nonempty-string-p title))
       "finding title is missing")
      ((not (safeslop-profiles--evaluation-nonempty-string-p consequence))
       "finding consequence is missing")
      ((not (listp finding-scopes))
       "finding scope_ids must be an array")
      ((cl-find-if-not
        (lambda (scope-id)
          (and (safeslop-profiles--evaluation-stable-id-p scope-id)
               (member scope-id scope-ids)))
        finding-scopes)
       "finding references an unknown credential scope")
      ((member outcome '("concern" "fail" "unknown"))
       (or (safeslop-profiles--evaluation-remediation-error remediation) nil))
      ((not (eq remediation :json-null))
       "bounded/pass/not_applicable finding remediation must be null")))))

(defun safeslop-profiles--evaluation-findings-error
    (findings allowed-axes scope-ids label &optional authority-order)
  "Validate FINDINGS for LABEL, ALLOWED-AXES, SCOPE-IDS, and AUTHORITY-ORDER."
  (catch 'invalid
    (unless (and (listp findings) findings)
      (throw 'invalid (format "%s findings must be a non-empty array" label)))
    (let ((seen-rules nil)
          (last-axis -1))
      (dolist (finding findings)
        (when-let* ((error (safeslop-profiles--evaluation-finding-error
                            finding allowed-axes scope-ids)))
          (throw 'invalid (format "%s: %s" label error)))
        (let ((rule-id (alist-get 'rule_id finding))
              (axis (alist-get 'axis finding)))
          (when (member rule-id seen-rules)
            (throw 'invalid (format "%s has duplicate rule_id" label)))
          (push rule-id seen-rules)
          (when authority-order
            (let ((axis-index (cl-position axis authority-order :test #'equal)))
              (when (< axis-index last-axis)
                (throw 'invalid "authority findings are not in engine axis order"))
              (setq last-axis axis-index))))))
    nil))

(defun safeslop-profiles--evaluation-authority-error (authority)
  "Return nil for a valid v1 AUTHORITY section, otherwise an error."
  (or
   (safeslop-profiles--evaluation-required-error
    authority '(findings credential_scopes) "authority")
   (catch 'invalid
     (let ((scopes (alist-get 'credential_scopes authority))
           (findings (alist-get 'findings authority))
           scope-ids)
       (unless (listp scopes)
         (throw 'invalid "credential_scopes must be an array"))
       (dolist (scope scopes)
         (when-let* ((error (safeslop-profiles--evaluation-credential-scope-error scope)))
           (throw 'invalid error))
         (let ((scope-id (alist-get 'scope_id scope)))
           (when (member scope-id scope-ids)
             (throw 'invalid "credential scope_id is duplicated"))
           (push scope-id scope-ids)))
       (when-let* ((error
                    (safeslop-profiles--evaluation-findings-error
                     findings safeslop-profiles--evaluation-authority-axes scope-ids
                     "authority" safeslop-profiles--evaluation-authority-axes)))
         (throw 'invalid error))
       (dolist (axis safeslop-profiles--evaluation-authority-axes)
         (unless (cl-find axis findings :key (lambda (finding) (alist-get 'axis finding))
                          :test #'equal)
           (throw 'invalid (format "authority core axis %s is missing" axis))))
       (dolist (scope-id scope-ids)
         (unless (cl-find-if (lambda (finding)
                              (member scope-id (alist-get 'scope_ids finding)))
                            findings)
           (throw 'invalid "credential scope is not referenced by a finding")))
       nil))))

(defun safeslop-profiles--evaluation-trust-error (trust scope-ids)
  "Return nil for a valid v1 TRUST section referencing SCOPE-IDS."
  (or
   (safeslop-profiles--evaluation-required-error
    trust '(state basis checked_at findings) "trust")
   (let ((state (alist-get 'state trust))
         (basis (alist-get 'basis trust))
         (checked-at (alist-get 'checked_at trust)))
     (or
      (cond
       ((not (member state safeslop-profiles--evaluation-trust-states))
        "trust state is unsupported")
       ((not (member basis safeslop-profiles--evaluation-trust-bases))
        "trust basis is unsupported")
       ((equal basis "unsaved")
        (unless (and (equal state "not_applicable") (eq checked-at :json-null))
          "unsaved trust must be N/A with a null timestamp"))
       ((equal basis "project_exact_bytes")
        (unless (and (member state '("trusted" "untrusted" "changed" "unknown"))
                     (safeslop-profiles--evaluation-timestamp-p checked-at))
          "exact-byte trust state/timestamp is malformed"))
       ((equal basis "embedded_builtin")
        (unless (and (member state '("trusted" "unknown"))
                     (safeslop-profiles--evaluation-timestamp-p checked-at))
          "builtin trust state/timestamp is malformed"))
       ((equal basis "unknown")
        (unless (and (equal state "unknown")
                     (safeslop-profiles--evaluation-timestamp-p checked-at))
          "unknown trust state/timestamp is malformed")))
      (safeslop-profiles--evaluation-findings-error
       (alist-get 'findings trust) '("trust") scope-ids "trust")))))

(defun safeslop-profiles--evaluation-readiness-error (readiness scope-ids)
  "Return nil for a valid v1 READINESS section referencing SCOPE-IDS."
  (or
   (safeslop-profiles--evaluation-required-error
    readiness '(state checked_at findings) "readiness")
   (let ((state (alist-get 'state readiness))
         (checked-at (alist-get 'checked_at readiness)))
     (or
      (cond
       ((not (member state safeslop-profiles--evaluation-readiness-states))
        "readiness state is unsupported")
       ((equal state "not_applicable")
        (unless (eq checked-at :json-null)
          "N/A readiness must have a null timestamp"))
       ((not (safeslop-profiles--evaluation-timestamp-p checked-at))
        "readiness timestamp is malformed"))
      (safeslop-profiles--evaluation-findings-error
       (alist-get 'findings readiness) '("readiness") scope-ids "readiness")))))

(defun safeslop-profiles--evaluation-validation-error (evaluation)
  "Validate EVALUATION against the closed v1 UI contract.
This helper is pure.  Return nil when supported and complete, or a loud-degrade
reason string.  Unknown rule IDs remain renderable; semantic identity belongs
to the engine, while this client validates only versioned shape, enums,
references, and engine array order."
  (or
   (safeslop-profiles--evaluation-required-error
    evaluation '(schema_version authority trust readiness) "evaluation")
   (if (not (equal (alist-get 'schema_version evaluation) 1))
       "evaluation schema_version is unsupported"
     (let* ((authority (alist-get 'authority evaluation))
            (scopes (and (safeslop-profiles--evaluation-object-p authority)
                         (alist-get 'credential_scopes authority)))
            (scope-ids (and (listp scopes)
                            (mapcar (lambda (scope) (alist-get 'scope_id scope)) scopes))))
       (or (safeslop-profiles--evaluation-authority-error authority)
           (safeslop-profiles--evaluation-trust-error
            (alist-get 'trust evaluation) scope-ids)
           (safeslop-profiles--evaluation-readiness-error
            (alist-get 'readiness evaluation) scope-ids))))))

(defun safeslop-profiles--evaluation-enum-label (value)
  "Return an explicit display label for closed enum VALUE."
  (if (equal value "not_applicable")
      "N/A"
    (upcase (replace-regexp-in-string "_" " " value t t))))

(defun safeslop-profiles--evaluation-outcome-face (outcome)
  "Return the reinforcing face for explicit OUTCOME text."
  (pcase outcome
    ("concern" 'safeslop-profile-evaluation-concern)
    ("bounded" 'safeslop-profile-evaluation-bounded)
    ("pass" 'safeslop-profile-evaluation-pass)
    ("fail" 'safeslop-profile-evaluation-fail)
    ("unknown" 'safeslop-profile-evaluation-unknown)
    ("not_applicable" 'safeslop-profile-evaluation-not-applicable)
    (_ 'safeslop-profile-evaluation-unknown)))

(defun safeslop-profiles--evaluation-outcome-token (outcome)
  "Return an explicit, faced outcome token for OUTCOME."
  (propertize (format "[%s]" (safeslop-profiles--evaluation-enum-label outcome))
              'face (safeslop-profiles--evaluation-outcome-face outcome)))

(defun safeslop-profiles--remediation-button-action (argument)
  "Dispatch a remediation button ARGUMENT using typed metadata only."
  (let ((metadata (if (listp argument)
                      argument
                    (button-get argument 'button-data))))
    (safeslop-profiles--dispatch-remediation
     (plist-get metadata :kind)
     (plist-get metadata :action-id)
     (plist-get metadata :docs-ref))))

(defun safeslop-profiles--evaluation-remediation-button (remediation)
  "Return a text button for typed REMEDIATION metadata."
  (let ((kind (alist-get 'kind remediation))
        (action-id (alist-get 'action_id remediation))
        (docs-ref (alist-get 'docs_ref remediation)))
    (make-text-button
     (format "[Guidance: %s]" action-id) nil
     'follow-link t
     'help-echo (format "%s · %s" kind docs-ref)
     'button-data (list :kind kind :action-id action-id :docs-ref docs-ref)
     'action #'safeslop-profiles--remediation-button-action)))

(defun safeslop-profiles--evaluation-render-finding (finding seen-actions)
  "Render FINDING and dedupe its typed action button through SEEN-ACTIONS."
  (let* ((outcome (alist-get 'outcome finding))
         (severity (alist-get 'severity finding))
         (remediation (alist-get 'remediation finding))
         (action-id (and (listp remediation) (alist-get 'action_id remediation)))
         (first-action (and action-id (not (gethash action-id seen-actions)))))
    (when first-action
      (puthash action-id t seen-actions))
    (concat
     "  " (safeslop-profiles--evaluation-outcome-token outcome)
     (format " %s (%s)\n" (alist-get 'title finding)
             (safeslop-profiles--evaluation-enum-label severity))
     (format "    %s\n" (alist-get 'consequence finding))
     (when (alist-get 'scope_ids finding)
       (format "    Scope IDs: %s\n"
               (string-join (alist-get 'scope_ids finding) ", ")))
     (when (listp remediation)
       (concat (format "    Guidance: %s\n" (alist-get 'summary remediation))
               (when first-action
                 (concat "    "
                         (safeslop-profiles--evaluation-remediation-button remediation)
                         "\n")))))))

(defun safeslop-profiles--evaluation-axis-label (axis)
  "Return the fixed display label for authority AXIS."
  (or (cdr (assoc axis '(("network" . "Network")
                         ("files" . "Files")
                         ("projection" . "Projection")
                         ("secrets" . "Secrets")
                         ("credentials" . "Credentials"))))
      axis))

(defun safeslop-profiles--evaluation-render-findings
    (findings seen-actions &optional authority-p)
  "Render FINDINGS in engine order, sharing SEEN-ACTIONS.
When AUTHORITY-P is non-nil, emit an axis label at each engine-ordered boundary."
  (let (parts previous-axis)
    (dolist (finding findings)
      (let ((axis (alist-get 'axis finding)))
        (when (and authority-p (not (equal axis previous-axis)))
          (push (format "\n%s\n" (safeslop-profiles--evaluation-axis-label axis)) parts)
          (setq previous-axis axis))
        (push (safeslop-profiles--evaluation-render-finding finding seen-actions) parts)))
    (apply #'concat (nreverse parts))))

(defun safeslop-profiles--evaluation-render-scopes (scopes)
  "Render value-free credential SCOPES in their engine order."
  (when scopes
    (concat
     "\nCredential scopes (value-free)\n"
     (mapconcat
      (lambda (scope)
        (format "  %s · %s · %s · %s · %s"
                (alist-get 'provider scope)
                (alist-get 'target scope)
                (safeslop-profiles--evaluation-enum-label (alist-get 'access scope))
                (safeslop-profiles--evaluation-enum-label (alist-get 'lifetime scope))
                (safeslop-profiles--evaluation-enum-label (alist-get 'basis scope))))
      scopes "\n")
     "\n")))

(defun safeslop-profiles--evaluation-checked-line (checked-at)
  "Render CHECKED-AT explicitly, including a null/not-collected snapshot."
  (if (eq checked-at :json-null)
      "Checked at: N/A (not collected)\n"
    (format "Checked at: %s\n" checked-at)))

(defun safeslop-profiles--evaluation-render-v1 (evaluation)
  "Purely render a validated v1 EVALUATION without deriving a combined verdict."
  (let* ((authority (alist-get 'authority evaluation))
         (trust (alist-get 'trust evaluation))
         (readiness (alist-get 'readiness evaluation))
         (seen-actions (make-hash-table :test #'equal)))
    (concat
     "Authority — what it can reach\n"
     (safeslop-profiles--evaluation-render-findings
      (alist-get 'findings authority) seen-actions t)
     (safeslop-profiles--evaluation-render-scopes
      (alist-get 'credential_scopes authority))
     "\nTrust — is this exact policy approved?\n"
     (format "State: %s\nBasis: %s\n"
             (safeslop-profiles--evaluation-enum-label (alist-get 'state trust))
             (safeslop-profiles--evaluation-enum-label (alist-get 'basis trust)))
     (safeslop-profiles--evaluation-checked-line (alist-get 'checked_at trust))
     (safeslop-profiles--evaluation-render-findings
      (alist-get 'findings trust) seen-actions)
     "\nReadiness — can this host launch it now?\n"
     (format "State: %s\n"
             (safeslop-profiles--evaluation-enum-label (alist-get 'state readiness)))
     (safeslop-profiles--evaluation-checked-line (alist-get 'checked_at readiness))
     "Caveat: this is a point-in-time local snapshot; remote authentication and authorization were not checked.\n"
     (safeslop-profiles--evaluation-render-findings
      (alist-get 'findings readiness) seen-actions))))

(defun safeslop-profiles--evaluation-render-unknown (error)
  "Render present but unsupported/malformed evaluation ERROR loudly."
  (concat
   "Profile safety evaluation\n"
   (propertize "UNKNOWN — update required" 'face 'safeslop-profile-evaluation-unknown)
   "\nAuthority, Trust, and Readiness cannot be classified from this engine evaluation.\n"
   (format "Reason: %s\n" error)
   "No legacy risk fallback was used because evaluation is present.\n"))

(defun safeslop-profiles--evaluation-render-legacy (data)
  "Render DATA's compatibility risk fields with an explicit legacy label."
  (let ((risk (alist-get 'risk data))
        (axes (alist-get 'risk_axes data)))
    (concat
     (propertize "Legacy safety summary — trust and readiness unavailable"
                 'face 'warning)
     "\n"
     (if (safeslop-profiles--evaluation-object-p risk)
         (concat
          (when (safeslop-profiles--evaluation-nonempty-string-p
                 (alist-get 'headline risk))
            (format "%s\n" (alist-get 'headline risk)))
          (mapconcat (lambda (line) (format "  %s" line))
                     (if (listp (alist-get 'lines risk)) (alist-get 'lines risk) nil)
                     "\n")
          (when (alist-get 'lines risk) "\n")
          (when (safeslop-profiles--evaluation-nonempty-string-p
                 (alist-get 'level risk))
            (format "Legacy level (compatibility only): %s\n" (alist-get 'level risk))))
       "Legacy risk data unavailable.\n")
     (when (listp axes)
       (mapconcat
        (lambda (axis)
          (format "  %s: %s (restricted: %s)"
                  (or (alist-get 'name axis) "unknown")
                  (or (alist-get 'value axis) "unknown")
                  (pcase (alist-get 'restricted axis)
                    ('t "yes")
                    (:json-false "no")
                    (_ "unknown"))))
        axes "\n"))
     (when axes "\n"))))

(defun safeslop-profiles--evaluation-text (data)
  "Purely validate and render DATA's v1 evaluation or labeled legacy fallback."
  (if-let* ((entry (assq 'evaluation data)))
      (let* ((evaluation (cdr entry))
             (error (safeslop-profiles--evaluation-validation-error evaluation)))
        (if error
            (safeslop-profiles--evaluation-render-unknown error)
          (safeslop-profiles--evaluation-render-v1 evaluation)))
    (safeslop-profiles--evaluation-render-legacy data)))

(provide 'safeslop-profile-evaluation)
;;; safeslop-profile-evaluation.el ends here
