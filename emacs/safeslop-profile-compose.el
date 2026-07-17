;;; safeslop-profile-compose.el --- Profile composition UI -*- lexical-binding: t; -*-

;; Copyright (C) 2026

;; Author: safeslop
;; Package-Requires: ((emacs "32.0"))
;; Keywords: tools, processes, ai

;;; Commentary:

;; Interactive profile composition backed by the safeslop catalog and CLI.
;; `safeslop-profiles.el' remains the public profile front.

;;; Code:

(require 'subr-x)
(require 'cl-lib)
(require 'safeslop-contract)
(require 'safeslop-client)
(require 'safeslop-surface)

;; Forward declarations for symbols owned by sibling profile modules.  These are
;; late-bound at runtime through `safeslop-profiles' front loading, so declaring
;; them keeps strict byte compilation warning-free without changing ownership.
(declare-function safeslop-profiles--valid-name-p "safeslop-profiles" (name))
(declare-function safeslop-profiles--join "safeslop-profiles" (values))
(declare-function safeslop-profiles--create-args "safeslop-profiles"
                  (name agent environment bundles packages network workspace
                        &optional no-default-bundle))
(declare-function safeslop-profiles--network-label "safeslop-profiles"
                  (environment network))
(declare-function safeslop-profiles--normalize-workspace "safeslop-profiles"
                  (workspace))
(declare-function safeslop-profiles--evaluation-text "safeslop-profile-evaluation"
                  (data))
(declare-function safeslop-profiles-create "safeslop-profiles"
                  (&optional name agent environment bundles packages network
                             workspace callback no-default-bundle))

(defvar safeslop-profiles--name-regexp)
(defvar safeslop-profiles--agents)
(defvar safeslop-profiles--environments)
(defvar safeslop-profiles--networks)

(defun safeslop-profiles--read-name (existing &optional default)
  "Read a new profile name, validating syntax and confirming overwrite.
EXISTING is the list of names already defined; choosing one of them prompts to
confirm the create-or-update overwrite.  DEFAULT, when given, is offered as the
initial value (used by clone)."
  (let ((name nil))
    (while (not name)
      (let ((candidate (string-trim
                        (read-string
                         (if default
                             (format "Profile name (default %s): " default)
                           "Profile name: ")
                         nil nil default))))
        (cond
         ((string-empty-p candidate)
          (message "Profile name must not be empty") (sit-for 1))
         ((not (safeslop-profiles--valid-name-p candidate))
          (message "Invalid name %S: start with a letter/underscore, then letters/digits/_/-"
                   candidate)
          (sit-for 1.5))
         ((and (member candidate existing)
               (not (yes-or-no-p (format "Profile %S already exists; overwrite it? "
                                         candidate))))
          nil) ; loop and read again
         (t (setq name candidate)))))
    name))

(defun safeslop-profiles--create-summary
    (verb name agent environment bundles packages network workspace no-default-bundle)
  "Return a one-line summary for confirming a profile create/update."
  (format "%s profile `%s' (%s, %s, %s; bundles=%s; packages=%s%s%s)? "
          verb name agent environment network
          (safeslop-profiles--join bundles)
          (safeslop-profiles--join packages)
          (if (and (stringp workspace) (not (string-empty-p workspace)))
              (format "; workspace=%s" workspace)
            "")
          (if no-default-bundle "; no default agent bundle" "")))

(defun safeslop-profiles--confirm-create
    (existing name agent environment bundles packages network workspace no-default-bundle)
  "Ask for final confirmation before the async profile create/update write."
  (yes-or-no-p
   (safeslop-profiles--create-summary
    (if (member name existing) "Update" "Create")
    name agent environment bundles packages network workspace no-default-bundle)))


;;; ---- compose buffer ------------------------------------------------------

(defconst safeslop-profiles-compose-buffer-name "*safeslop profile compose*"
  "Buffer name for profile creation composition.")

(defvar-local safeslop-profiles-compose--state nil
  "Current profile compose state as an alist.")

(defun safeslop-profiles--alist-index (rows)
  "Return an alist mapping each row name in ROWS to its row alist."
  (mapcar (lambda (row) (cons (alist-get 'name row) row)) (append rows nil)))

(defun safeslop-profiles--catalog-indexes (bundle-data package-data)
  "Merge bundle and package catalog envelope DATA into lookup indexes."
  (list (cons 'bundles (safeslop-profiles--alist-index (alist-get 'bundles bundle-data)))
        (cons 'packages (safeslop-profiles--alist-index (alist-get 'packages package-data)))
        (cons 'defaults (alist-get 'defaults bundle-data))))

(defun safeslop-profiles--lookup-default-bundle (agent catalog)
  "Return AGENT's default bundle from CATALOG, falling back to a same-named bundle."
  (let* ((defaults (alist-get 'defaults catalog))
         (key (and agent (intern-soft agent)))
         (from-defaults (or (and key (alist-get key defaults))
                            (alist-get agent defaults nil nil #'string=))))
    (or from-defaults
        (when (assoc agent (alist-get 'bundles catalog)) agent))))

(defun safeslop-profiles--catalog-row (kind name catalog)
  "Return catalog row NAME from KIND (`bundles' or `packages') in CATALOG."
  (cdr (assoc name (alist-get kind catalog))))

(defun safeslop-profiles--row-vector (row field)
  "Return ROW FIELD as a list, accepting JSON vectors."
  (append (or (alist-get field row) []) nil))

(defun safeslop-profiles--put-package-source (rows name source locked)
  "Put package NAME in ROWS with SOURCE, preserving stronger existing locks."
  (let ((existing (assoc name rows)))
    (if existing
        (let ((cell (cdr existing)))
          (unless (alist-get 'locked cell)
            (setcdr existing (list (cons 'source source) (cons 'locked locked) (cons 'checked t))))
          rows)
      (cons (cons name (list (cons 'source source) (cons 'locked locked) (cons 'checked t))) rows))))

(defun safeslop-profiles--expand-requires (name rows catalog seen)
  "Recursively add NAME package requirements to ROWS using CATALOG, tracking SEEN."
  (if (member name seen)
      rows
    (let ((pkg (safeslop-profiles--catalog-row 'packages name catalog))
          (seen (cons name seen)))
      (dolist (req (safeslop-profiles--row-vector pkg 'requires) rows)
        (setq rows (safeslop-profiles--put-package-source rows req (format "requires:%s" name) t))
        (setq rows (safeslop-profiles--expand-requires req rows catalog seen))))))

(defun safeslop-profiles--package-rows (agent bundles packages no-default-bundle catalog)
  "Return catalog package rows for AGENT, BUNDLES, direct PACKAGES and CATALOG."
  (let ((rows nil)
        (selected-bundles (copy-sequence (or bundles nil))))
    (unless no-default-bundle
      (when-let* ((default (safeslop-profiles--lookup-default-bundle agent catalog)))
        (push (cons default 'default) selected-bundles)))
    (dolist (bundle selected-bundles)
      (let* ((name (if (consp bundle) (car bundle) bundle))
             (source-kind (if (and (consp bundle) (eq (cdr bundle) 'default)) "default" "bundle"))
             (bundle-row (safeslop-profiles--catalog-row 'bundles name catalog)))
        (dolist (pkg (safeslop-profiles--row-vector bundle-row 'packages))
          (setq rows (safeslop-profiles--put-package-source rows pkg (format "%s:%s" source-kind name) t)))))
    (dolist (pkg packages)
      (setq rows (safeslop-profiles--put-package-source rows pkg "direct" nil)))
    (dolist (pkg (mapcar #'car rows))
      (setq rows (safeslop-profiles--expand-requires pkg rows catalog nil)))
    (dolist (pkg (alist-get 'packages catalog))
      (unless (assoc (car pkg) rows)
        (push (cons (car pkg) (list (cons 'source nil) (cons 'locked nil) (cons 'checked nil))) rows)))
    (sort rows (lambda (a b) (string< (car a) (car b))))))

(defun safeslop-profiles--bundle-rows (agent bundles no-default-bundle catalog)
  "Return catalog bundle rows with selected/default lock metadata."
  (let ((default (unless no-default-bundle
                   (safeslop-profiles--lookup-default-bundle agent catalog))))
    (mapcar (lambda (bundle)
              (let* ((name (car bundle))
                     (is-default (and default (string= name default))))
                (cons name (list (cons 'checked (or is-default (member name bundles)))
                                 (cons 'locked is-default)
                                 (cons 'source (when is-default (format "default:%s" name)))))))
            (alist-get 'bundles catalog))))

(defun safeslop-profiles--bundle-suggestions (&optional directory)
  "Return suggested bundle names from local project markers in DIRECTORY."
  (let ((dir (file-name-as-directory (or directory default-directory)))
        (markers '(("go.mod" . "go")
                   ("package.json" . "web")
                   ("pyproject.toml" . "python")
                   ("Cargo.toml" . "rust"))))
    (delq nil (mapcar (lambda (m)
                        (when (file-exists-p (expand-file-name (car m) dir))
                          (cdr m)))
                      markers))))

(defun safeslop-profiles--compose-state
    (name agent environment bundles packages network workspace no-default-bundle catalog)
  "Build a pure profile compose state and derived package rows."
  (let ((suggestions (safeslop-profiles--bundle-suggestions)))
    (list (cons 'name name)
          (cons 'agent agent)
          (cons 'environment environment)
          (cons 'bundles bundles)
          (cons 'packages packages)
          (cons 'network network)
          (cons 'workspace workspace)
          (cons 'no-default-bundle no-default-bundle)
          (cons 'catalog catalog)
          (cons 'suggestions suggestions)
          (cons 'package-rows (safeslop-profiles--package-rows
                               agent bundles packages no-default-bundle catalog)))))

(defun safeslop-profiles--compose-args (state)
  "Return profile create argv for compose STATE."
  (safeslop-profiles--create-args
   (alist-get 'name state) (alist-get 'agent state) (alist-get 'environment state)
   (alist-get 'bundles state) (alist-get 'packages state)
   (alist-get 'network state) (alist-get 'workspace state)
   (alist-get 'no-default-bundle state)))

(defun safeslop-profiles--dry-run-args (state)
  "Return dry-run profile create argv for compose STATE."
  (let ((args (safeslop-profiles--compose-args state)))
    (append (butlast args 2) '("--dry-run") (last args 2))))

(defvar safeslop-profiles-compose-mode-map
  (let ((map (make-sparse-keymap)))
    (define-key map (kbd "RET") #'safeslop-profiles-compose-toggle)
    (define-key map (kbd "?") #'safeslop-profiles-compose-help)
    (define-key map (kbd "g") #'safeslop-profiles-compose-refresh)
    (define-key map (kbd "C-c C-c") #'safeslop-profiles-compose-preview-save)
    (define-key map (kbd "q") #'safeslop-profiles-compose-cancel)
    map)
  "Keymap for `safeslop-profiles-compose-mode'.")

(define-derived-mode safeslop-profiles-compose-mode special-mode "safeslop-profile-compose"
  "Major mode for composing a safeslop profile before save.")

(defun safeslop-profiles-compose--insert-row (type name checked locked source)
  "Insert one compose row and attach row metadata."
  (let ((start (point)))
    (insert (if (eq type 'bundle)
                (format "[%s] %s bundle %-18s %s\n"
                        (if checked "x" " ") (if locked "L" " ") name (or source ""))
              (format "[%s] %s %-18s package %s\n"
                      (if checked "x" " ") (if locked "L" " ") name (or source ""))))
    (put-text-property start (point) 'safeslop-row (list (cons 'type type) (cons 'name name)))))

(defun safeslop-profiles-compose--insert-default-bundle-control (name disabled)
  "Insert the automatic bundle control for NAME, disabled when DISABLED."
  (let ((start (point)))
    (insert (format "Automatic agent bundle: [%s] %s (%s)\n"
                    (if disabled " " "x") name (if disabled "disabled" "enabled")))
    (put-text-property start (point) 'safeslop-row
                       (list (cons 'type 'default-bundle) (cons 'name name)))
    (when disabled
      (insert "  Warning: automatic agent runtime packages are omitted; the agent may not launch.\n"))))

(defun safeslop-profiles-compose--insert-field (name label value)
  "Insert editable compose field NAME with displayed LABEL and VALUE."
  (let ((start (point)))
    (insert (format "%s: %s  [RET edit]\n" label (or value "")))
    (put-text-property start (point) 'safeslop-row
                       (list (cons 'type 'field) (cons 'name name)))))

(defun safeslop-profiles-compose--render ()
  "Render the current compose state."
  (let* ((inhibit-read-only t)
         (state safeslop-profiles-compose--state)
         (default (safeslop-profiles--lookup-default-bundle
                   (alist-get 'agent state) (alist-get 'catalog state))))
    (erase-buffer)
    (insert "safeslop Profiles compose buffer\n")
    (insert "Keys: RET edit/toggle, ? help, g refresh catalog, C-c C-c preview/save, q cancel; L = included by source\n\n")
    (insert "Fields (RET edits):\n")
    (safeslop-profiles-compose--insert-field 'name "Name" (alist-get 'name state))
    (safeslop-profiles-compose--insert-field 'agent "Agent" (alist-get 'agent state))
    (safeslop-profiles-compose--insert-field 'environment "Environment" (alist-get 'environment state))
    (safeslop-profiles-compose--insert-field
     'network "Network"
     (safeslop-profiles--network-label
      (alist-get 'environment state) (alist-get 'network state)))
    (safeslop-profiles-compose--insert-field 'workspace "Workspace" (alist-get 'workspace state))
    (insert "\n")
    (when (and (equal (alist-get 'environment state) "container")
               (equal (alist-get 'network state) "deny"))
      (insert "  Passive denied-destination review is operator-opened; it grants nothing automatically.\n"))
    (when default
      (safeslop-profiles-compose--insert-default-bundle-control
       default (alist-get 'no-default-bundle state)))
    (insert "Bundles (suggested rows are visible but not preselected):\n")
    (dolist (bundle (safeslop-profiles--bundle-rows
                     (alist-get 'agent state) (alist-get 'bundles state)
                     (alist-get 'no-default-bundle state) (alist-get 'catalog state)))
      (let* ((name (car bundle))
             (source (alist-get 'source (cdr bundle)))
             (suggested (member name (alist-get 'suggestions state))))
        (safeslop-profiles-compose--insert-row
         'bundle name (alist-get 'checked (cdr bundle)) (alist-get 'locked (cdr bundle))
         (string-join (delq nil (list source (when suggested "suggested"))) ", "))))
    (insert "\nPackages:\n")
    (dolist (pkg (alist-get 'package-rows state))
      (safeslop-profiles-compose--insert-row
       'package (car pkg) (alist-get 'checked (cdr pkg))
       (alist-get 'locked (cdr pkg)) (alist-get 'source (cdr pkg))))
    (goto-char (point-min))))

(defun safeslop-profiles-compose--row-at-point ()
  "Return compose row metadata at point."
  (or (get-text-property (point) 'safeslop-row)
      (get-text-property (max (point-min) (1- (point))) 'safeslop-row)))

(defun safeslop-profiles-compose--row-at-position (position)
  "Return compose row metadata at POSITION in the current buffer."
  (save-excursion
    (goto-char (max (point-min) (min position (point-max))))
    (safeslop-profiles-compose--row-at-point)))

(defun safeslop-profiles-compose--find-row (row)
  "Return the current position of logical compose ROW, or nil when absent."
  (when row
    (save-excursion
      (let ((position (point-min))
            found)
        (while (and (< position (point-max)) (not found))
          (when (equal row (get-text-property position 'safeslop-row))
            (setq found position))
          (setq position (next-single-property-change
                          position 'safeslop-row nil (point-max))))
        found))))

(defun safeslop-profiles-compose--capture-context ()
  "Capture logical point and scroll rows for every window showing this buffer."
  (list :point-row (safeslop-profiles-compose--row-at-point)
        :point (point)
        :views
        (mapcar
         (lambda (window)
           (list :window window
                 :point-row (safeslop-profiles-compose--row-at-position
                             (window-point window))
                 :point (window-point window)
                 :start-row (safeslop-profiles-compose--row-at-position
                             (window-start window))
                 :start (window-start window)))
         (get-buffer-window-list (current-buffer) nil t))))

(defun safeslop-profiles-compose--restore-context (context)
  "Restore logical point and scroll rows from compose CONTEXT after rendering."
  (let ((point (or (safeslop-profiles-compose--find-row
                    (plist-get context :point-row))
                   (plist-get context :point))))
    (goto-char (max (point-min) (min point (point-max)))))
  (dolist (view (plist-get context :views))
    (let ((window (plist-get view :window)))
      (when (window-live-p window)
        (let ((point (or (safeslop-profiles-compose--find-row
                          (plist-get view :point-row))
                         (plist-get view :point)))
              (start (or (safeslop-profiles-compose--find-row
                          (plist-get view :start-row))
                         (plist-get view :start))))
          (set-window-point window (max (point-min) (min point (point-max))))
          (set-window-start window (max (point-min) (min start (point-max))) t))))))

(defun safeslop-profiles-compose--render-preserving-context ()
  "Render compose state without moving an operator away from its logical row."
  (let ((context (safeslop-profiles-compose--capture-context)))
    (safeslop-profiles-compose--render)
    (safeslop-profiles-compose--restore-context context)))

(defun safeslop-profiles-compose--locked-message (name row)
  "Explain why compose ROW named NAME cannot be directly toggled."
  (let ((source (or (alist-get 'source (cdr row)) "an inherited selection")))
    (message (if (and (stringp source) (string-prefix-p "default:" source))
                 "safeslop: %s is locked because it is included by %s; use Automatic agent bundle to omit it"
               "safeslop: %s is locked because it is included by %s; toggle that source instead")
             name source)))

(defun safeslop-profiles-compose--set-field (field value)
  "Set compose FIELD to VALUE after its local UI validation.
The engine remains the authoritative policy validator at dry-run/save time."
  (let ((state safeslop-profiles-compose--state))
    (pcase field
      ('name
       (unless (safeslop-profiles--valid-name-p value)
         (user-error "Profile name must match %s" safeslop-profiles--name-regexp)))
      ('agent
       (unless (member value safeslop-profiles--agents)
         (user-error "Unsupported agent: %s" value)))
      ('environment
       (unless (member value safeslop-profiles--environments)
         (user-error "Unsupported environment: %s" value)))
      ('network
       (unless (member value safeslop-profiles--networks)
         (user-error "Unsupported network policy: %s" value)))
      ('workspace (setq value (safeslop-profiles--normalize-workspace value)))
      (_ (user-error "Unknown compose field: %s" field)))
    (setcdr (assq field state) value)
    (when (eq field 'agent)
      (setcdr (assq 'package-rows state)
              (safeslop-profiles--package-rows
               (alist-get 'agent state) (alist-get 'bundles state) (alist-get 'packages state)
               (alist-get 'no-default-bundle state) (alist-get 'catalog state))))))

(defun safeslop-profiles-compose-edit-field (field)
  "Prompt for and apply FIELD in the current creation compose state."
  (interactive)
  (let* ((state safeslop-profiles-compose--state)
         (old (or (alist-get field state) ""))
         (value
          (pcase field
            ('name (read-string "Profile name: " old))
            ('agent (completing-read "Agent: " safeslop-profiles--agents nil t nil nil old))
            ('environment (completing-read "Environment: " safeslop-profiles--environments nil t nil nil old))
            ('network (completing-read "Network: " safeslop-profiles--networks nil t nil nil old))
            ('workspace (read-string "Workspace (empty for default): " old))
            (_ (user-error "Unknown compose field: %s" field)))))
    (safeslop-profiles-compose--set-field field value)
    (safeslop-profiles-compose--render-preserving-context)))

(defun safeslop-profiles-compose-toggle ()
  "Toggle a selectable row or edit a field at point."
  (interactive)
  (let* ((row (safeslop-profiles-compose--row-at-point))
         (type (alist-get 'type row))
         (name (alist-get 'name row))
         (state safeslop-profiles-compose--state)
         changed)
    (pcase type
      ('field (safeslop-profiles-compose-edit-field name))
      ('default-bundle
       (let ((default (safeslop-profiles--lookup-default-bundle
                       (alist-get 'agent state) (alist-get 'catalog state))))
         (if (equal name default)
             (progn
               (setcdr (assoc 'no-default-bundle state)
                       (not (alist-get 'no-default-bundle state)))
               (setq changed t))
           (message "safeslop: no automatic agent bundle is available"))))
      ('bundle
       (let ((bundle (assoc name (safeslop-profiles--bundle-rows
                                  (alist-get 'agent state) (alist-get 'bundles state)
                                  (alist-get 'no-default-bundle state) (alist-get 'catalog state)))))
         (if (alist-get 'locked (cdr bundle))
             (safeslop-profiles-compose--locked-message name bundle)
           (let ((bundles (alist-get 'bundles state)))
             (setcdr (assoc 'bundles state)
                     (if (member name bundles) (remove name bundles) (cons name bundles)))
             (setq changed t)))))
      ('package
       (let ((pkg (assoc name (alist-get 'package-rows state))))
         (if (alist-get 'locked (cdr pkg))
             (safeslop-profiles-compose--locked-message name pkg)
           (let ((packages (alist-get 'packages state)))
             (setcdr (assoc 'packages state)
                     (if (member name packages) (remove name packages) (cons name packages)))
             (setq changed t)))))
      (_ (message "safeslop: no selectable row at point")))
    (when changed
      (setcdr (assoc 'package-rows state)
              (safeslop-profiles--package-rows
               (alist-get 'agent state) (alist-get 'bundles state) (alist-get 'packages state)
               (alist-get 'no-default-bundle state) (alist-get 'catalog state)))
      (safeslop-profiles-compose--render-preserving-context))))

(defun safeslop-profiles--package-help (pkg)
  "Return help text for package catalog row PKG."
  (string-join
   (delq nil (list (format "%s (%s)" (alist-get 'name pkg) (or (alist-get 'kind pkg) "package"))
                   (when (alist-get 'version pkg) (format "version: %s" (alist-get 'version pkg)))
                   (when (alist-get 'requires pkg) (format "requires: %s" (safeslop-profiles--join (safeslop-profiles--row-vector pkg 'requires))))
                   (when (alist-get 'conflicts pkg) (format "conflicts: %s" (safeslop-profiles--join (safeslop-profiles--row-vector pkg 'conflicts))))
                   (when (alist-get 'runtimeEgress pkg) (format "runtime egress: %s" (safeslop-profiles--join (safeslop-profiles--row-vector pkg 'runtimeEgress))))
                   (when (alist-get 'note pkg) (format "note: %s" (alist-get 'note pkg)))))
   "; "))

(defun safeslop-profiles-compose-help ()
  "Show help for the bundle or package row at point."
  (interactive)
  (let* ((row (safeslop-profiles-compose--row-at-point))
         (type (alist-get 'type row))
         (name (alist-get 'name row))
         (catalog (alist-get 'catalog safeslop-profiles-compose--state)))
    (message "%s"
             (pcase type
               ('bundle (let ((bundle (safeslop-profiles--catalog-row 'bundles name catalog)))
                          (format "%s: %s; packages: %s" name
                                  (or (alist-get 'description bundle) "")
                                  (safeslop-profiles--join (safeslop-profiles--row-vector bundle 'packages)))))
               ('package (safeslop-profiles--package-help
                          (safeslop-profiles--catalog-row 'packages name catalog)))
               ('default-bundle
                (format "Automatic %s bundle is %s; RET toggles automatic inclusion. Explicit selections stay selected, but the agent may not launch without its runtime."
                        name (if (alist-get 'no-default-bundle safeslop-profiles-compose--state)
                                 "disabled" "enabled")))
               (_ "No row help here")))))

(defun safeslop-profiles--fetch-compose-catalog ()
  "Synchronously fetch catalog bundle/package data for compose."
  (let ((bundles (safeslop--call-json '("catalog" "list" "--bundles" "--output" "json")))
        (packages (safeslop--call-json '("catalog" "list" "--output" "json"))))
    (safeslop-profiles--catalog-indexes
     (and (safeslop-contract-ok-p bundles) (safeslop-contract-data bundles))
     (and (safeslop-contract-ok-p packages) (safeslop-contract-data packages)))))

(defun safeslop-profiles-compose-open ()
  "Open the interactive profile compose buffer."
  (interactive)
  (let ((buf (get-buffer-create safeslop-profiles-compose-buffer-name)))
    (with-current-buffer buf
      (safeslop-profiles-compose-mode)
      (setq safeslop-profiles-compose--state
            (safeslop-profiles--compose-state
             "review" "claude" "container" nil nil "deny" "." nil
             (safeslop-profiles--fetch-compose-catalog)))
      (safeslop-profiles-compose--render))
    (pop-to-buffer-same-window buf)
    buf))

(defun safeslop-profiles-compose-refresh ()
  "Refresh catalog data for the compose buffer."
  (interactive)
  (setcdr (assoc 'catalog safeslop-profiles-compose--state)
          (safeslop-profiles--fetch-compose-catalog))
  (setcdr (assoc 'package-rows safeslop-profiles-compose--state)
          (safeslop-profiles--package-rows
           (alist-get 'agent safeslop-profiles-compose--state)
           (alist-get 'bundles safeslop-profiles-compose--state)
           (alist-get 'packages safeslop-profiles-compose--state)
           (alist-get 'no-default-bundle safeslop-profiles-compose--state)
           (alist-get 'catalog safeslop-profiles-compose--state)))
  (safeslop-profiles-compose--render-preserving-context)
  (message "safeslop: catalog refreshed"))

(defun safeslop-profiles-compose-cancel ()
  "Cancel profile compose without writing."
  (interactive)
  (kill-buffer (current-buffer)))

(defun safeslop-profiles--preview-text (data)
  "Render engine-authored dry-run DATA for confirmation."
  (let ((resolved (alist-get 'resolved data)))
    (string-join
     (delq nil
           (list "Engine safety preview"
                 (safeslop-profiles--evaluation-text data)
                 (format "resolved packages: %s"
                         (safeslop-profiles--join
                          (safeslop-profiles--row-vector resolved 'identitySet)))
                 (when (alist-get 'recipeID data)
                   (format "recipe: %s" (alist-get 'recipeID data)))))
     "\n")))

(defun safeslop-profiles--show-preview (args env)
  "Display dry-run preview ENV for ARGS and return its text."
  (let ((text (safeslop-profiles--preview-text (safeslop-contract-data env))))
    (with-current-buffer (get-buffer-create "*safeslop profile preview*")
      (let ((inhibit-read-only t))
        (special-mode)
        (erase-buffer)
        (insert (safeslop-surface--breadcrumb args))
        (insert text)
        (insert "\n"))
      (display-buffer (current-buffer)))
    text))

(defun safeslop-profiles-compose-preview-save ()
  "Preview exact compose state with the engine, then write after explicit yes."
  (interactive)
  (let* ((state safeslop-profiles-compose--state)
         (args (safeslop-profiles--dry-run-args state)))
    (safeslop--call-json-async
     args
     (lambda (env)
       (if (not (safeslop-contract-ok-p env))
           (safeslop--show-envelope-buffer "*safeslop profile preview*" args env)
         (safeslop-profiles--show-preview args env)
         (when (yes-or-no-p "Save this profile after the engine safety preview? ")
           (safeslop-profiles-create
            (alist-get 'name state) (alist-get 'agent state) (alist-get 'environment state)
            (alist-get 'bundles state) (alist-get 'packages state)
            (alist-get 'network state) (alist-get 'workspace state) nil
            (alist-get 'no-default-bundle state))))))))


(provide 'safeslop-profile-compose)
;;; safeslop-profile-compose.el ends here
