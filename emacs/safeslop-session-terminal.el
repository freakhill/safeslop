;;; safeslop-session-terminal.el --- Session terminal support -*- lexical-binding: t; -*-

;; Copyright (C) 2026

;; Author: safeslop
;; Package-Requires: ((emacs "32.0"))
;; Keywords: tools, processes, ai

;;; Commentary:

;; PTY launch, fallback monitoring, failure reporting, and live safety chrome.
;; `safeslop-session.el' remains the public session front.

;;; Code:

(require 'subr-x)
(require 'seq)
(require 'cl-lib)
(require 'term)
(require 'safeslop-contract)
(require 'safeslop-client)
(require 'safeslop-surface)
(require 'safeslop-output)

(declare-function safeslop-portal--reveal-session "safeslop-portal" (id))
(declare-function safeslop-portal "safeslop-portal" ())
(declare-function safeslop-session-detail "safeslop-session" (&optional session-id data))
(declare-function safeslop-session--read-id "safeslop-session" (prompt))
(declare-function eat-mode "eat" ())
(declare-function eat-exec "eat" (buffer name command startfile switches))
(defvar eat-term-name)
(defvar compilation-mode-map)

(defvar-local safeslop-session--run-output nil
  "Raw stdout accumulated from the `session run' process for this buffer.
Captured before term-mode renders it, so PTY_UNAVAILABLE detection is immune to
terminal line wrapping and term's trailing status line.")

(defvar-local safeslop-session--fallback-done nil
  "Non-nil once this run buffer has switched to the JSONL status fallback.
Guards against the run process's filter and sentinel both triggering the switch.")

(defun safeslop-session--pty-unavailable-p (output)
  "Return non-nil if OUTPUT carries the PTY_UNAVAILABLE contract error
code. A token match on the stable error code, not a strict JSON parse:
the run process is interactive, so its stdout may carry agent banner
text around the envelope and a PTY translates newlines, either of which
can defeat a whole-buffer parse."
  (and (stringp output)
       (string-match-p "\"PTY_UNAVAILABLE\"" output)))

(defun safeslop-session--maybe-status-fallback (buf session-id)
  "Switch BUF to the JSONL status fallback if its run reported PTY_UNAVAILABLE.
Idempotent per buffer via `safeslop-session--fallback-done'."
  (when (buffer-live-p buf)
    (with-current-buffer buf
      (when (and (not safeslop-session--fallback-done)
                 (safeslop-session--pty-unavailable-p safeslop-session--run-output))
        (setq safeslop-session--fallback-done t)
        (safeslop-session-status-fallback session-id)))))

(defconst safeslop-session--failure-text-width 160
  "Maximum displayed width of one structured failure summary or action.")

(defconst safeslop-session--failure-unsafe-patterns
  '("op://" "begin .*key" "seeded-secret" "\\btoken\\b"
    "/Users/" "/home/" "/tmp/" "\\.ssh/" "\\.aws/" "\\.kube/")
  "Case-insensitive patterns suppressed from failure UI as a defensive backstop.")

(defun safeslop-session--failure-safe-text (value)
  "Return bounded one-line VALUE when display-safe, otherwise nil."
  (when (and (stringp value) (not (string-empty-p value)))
    (let* ((one-line (string-trim
                      (replace-regexp-in-string "[[:cntrl:]]+" " " value)))
           (case-fold-search t))
      (unless (or (string-empty-p one-line)
                  (cl-some (lambda (re) (string-match-p re one-line))
                           safeslop-session--failure-unsafe-patterns))
        (truncate-string-to-width one-line safeslop-session--failure-text-width
                                  nil nil "…")))))

(defun safeslop-session--structured-failure (data)
  "Return DATA's validated, value-free v1 `last_failure' alist, or nil."
  (let* ((raw (and (listp data) (alist-get 'last_failure data)))
         (version (and (listp raw) (alist-get 'version raw)))
         (code (and (listp raw) (alist-get 'code raw)))
         (summary (and (listp raw)
                       (safeslop-session--failure-safe-text
                        (alist-get 'summary raw))))
         (action (and (listp raw)
                      (safeslop-session--failure-safe-text
                       (alist-get 'action raw)))))
    (when (and (equal version 1) (stringp code)
               (string-match-p "\\`[a-z0-9_]+\\'" code)
               summary action)
      `((version . ,version) (code . ,code)
        (summary . ,summary) (action . ,action)))))

(defun safeslop-session--failure-summary (data)
  "Return structured failure summary from DATA, with safe legacy fallback."
  (or (alist-get 'summary (safeslop-session--structured-failure data))
      (safeslop-session--failure-safe-text (alist-get 'last_error data))))

(defvar safeslop-session--reported-failures (make-hash-table :test #'equal)
  "Failure notification keys already shown during this Emacs process.")

(defun safeslop-session--report-terminal-failure (session-id)
  "Show SESSION-ID's stored failure once and refresh a live portal."
  (when-let* ((data (safeslop-session--fetch-data session-id))
              (reason (safeslop-session--failure-summary data)))
    (let* ((failure (safeslop-session--structured-failure data))
           (action (alist-get 'action failure))
           (key (if failure
                    (list session-id (alist-get 'version failure)
                          (alist-get 'code failure))
                  (list session-id 'legacy reason))))
      (unless (gethash key safeslop-session--reported-failures)
        (puthash key t safeslop-session--reported-failures)
        (message "safeslop session failed: %s%s"
                 reason (if action (concat " " action) ""))
        (safeslop-session-detail session-id data)
        (when (fboundp 'safeslop-portal--reveal-session)
          (safeslop-portal--reveal-session session-id))))))

;;; Self-describing live buffers (specs/0086 T3) -----------------------------
;; A live agent buffer used to be named by opaque session id (`*safeslop-<id>*'),
;; so an operator with several sessions could not answer "which buffer, which
;; project, which credentials?" without opening details one by one (specs/0071 rec
;; #3).  These pure builders derive a value-free, self-describing buffer label and
;; header line from a `session status' record; the buffer keys on a buffer-local
;; id so the portal still finds it after the name becomes descriptive.

(defvar-local safeslop-session-id nil
  "The safeslop session id this terminal buffer is running, or nil.
Set after terminal creation so the portal can find a live buffer by id even
after its displayed name becomes descriptive (specs/0086 T3).  The id is the
sole addressing handle; the buffer name is a pure, renamable label.")

(defvar-local safeslop-session-safety-chrome nil
  "Buffer-local mode-line safety posture for a live safeslop session.
The symbol itself is installed in `mode-line-format', so refreshing this value
updates the segment without replacing terminal-mode or user entries.")

(defun safeslop-session--cell-help (cell)
  "Return CELL's help text, or nil when it has none."
  (and (stringp cell) (get-text-property 0 'help-echo cell)))

(defun safeslop-session--posture-help (data)
  "Return value-free expanded safety posture help for session DATA.
Environment and network descriptions come from the same shared cells used by
the dashboards.  Credential text uses the defensive 0086 scope formatter."
  (let* ((env (or (safeslop-session--safe-display-field
                   (safeslop-session--field data 'environment))
                  "unknown"))
         (net (or (safeslop-session--safe-display-field
                   (safeslop-session--field data 'network))
                  "unknown"))
         (env-help (safeslop-session--cell-help
                    (safeslop-surface--env-cell env)))
         (net-help (safeslop-session--cell-help
                    (safeslop-surface--net-cell net))))
    (string-join
     (list (if env-help
               (format "environment=%s (%s)" env env-help)
             (format "environment=%s" env))
           (or net-help (format "network=%s" net))
           (format "credentials: %s" (safeslop-session--creds-summary data)))
     " · ")))

(defun safeslop-session--safety-chrome (data)
  "Return a persistent, color-redundant mode-line segment for session DATA.
Literal environment/network words remain readable without color; the existing
shared faces reinforce them.  Help text expands the same value-free posture."
  (let* ((env (or (safeslop-session--safe-display-field
                   (safeslop-session--field data 'environment))
                  "unknown"))
         (net (or (safeslop-session--safe-display-field
                   (safeslop-session--field data 'network))
                  "unknown"))
         (count (safeslop-session--credential-count data))
         (chrome (concat "safeslop["
                         (safeslop-surface--env-cell env) "/"
                         (safeslop-surface--net-cell net)
                         " creds:" (if (> count 0) (number-to-string count) "none")
                         "]")))
    (add-text-properties 0 (length chrome)
                         (list 'help-echo (safeslop-session--posture-help data))
                         chrome)
    chrome))

(defun safeslop-session--install-safety-chrome (data)
  "Install DATA's safety segment locally without replacing the current mode line.
Installation is idempotent: the buffer-local segment symbol appears once and a
copy of the terminal mode's existing format remains after it."
  (setq-local safeslop-session-safety-chrome
              (safeslop-session--safety-chrome data))
  (let ((format (cond ((listp mode-line-format) (copy-sequence mode-line-format))
                      ((null mode-line-format) nil)
                      (t (list mode-line-format)))))
    (unless (memq 'safeslop-session-safety-chrome format)
      (setq-local mode-line-format
                  (cons 'safeslop-session-safety-chrome format)))))

(defconst safeslop-session--creds-unsafe-patterns
  '("op://" "\\benv:" "private[-_ ]?key" "begin .*key" "\\btoken\\b" "\\`[~/]")
  "Case-insensitive patterns never rendered from credential scope fields.
Mirrors `safeslop-portal--creds-unsafe-patterns' (specs/0086 T2): the header
is a second credential-scope surface, so it keeps the same defensive
value-free floor even if a future JSON envelope regresses and carries a
ref/value/path.")

(defun safeslop-session--field (data key)
  "Return DATA's KEY as a trimmed display string, or nil when empty/absent."
  (let ((v (alist-get key data)))
    (when (and (stringp v) (not (string-empty-p v))) v)))

(defun safeslop-session--safe-display-field (value)
  "Return VALUE when it is non-empty and safe to render, else nil.
Refs, token markers, staged paths, and key refs are suppressed defensively so
a buffer name/header never renders them even if the JSON regresses
(specs/0086 T3)."
  (when (and (stringp value) (not (string-empty-p value)))
    (let ((case-fold-search t))
      (unless (cl-some (lambda (re) (string-match-p re value))
                       safeslop-session--creds-unsafe-patterns)
        value))))

(defun safeslop-session--creds-safe-field (value)
  "Return VALUE when it is a safe credential-scope display field."
  (safeslop-session--safe-display-field value))

(defun safeslop-session--creds-scope-string (scope)
  "Return one credential SCOPE alist as a compact \"kind name scope\" string.
Only the engine's non-secret kind/name/scope fields are used; empty or unsafe
fields are dropped so nothing but value-free text reaches the header."
  (string-join
   (delq nil
         (mapcar (lambda (key)
                   (safeslop-session--creds-safe-field (alist-get key scope)))
                 '(kind name scope)))
   " "))

(defun safeslop-session--credential-scope-strings (data)
  "Return DATA's non-empty, defensively value-free credential scope strings."
  (let ((scopes (let ((v (alist-get 'credential_scopes data)))
                  (if (vectorp v) (append v nil) v))))
    (delq nil
          (mapcar (lambda (scope)
                    (let ((str (safeslop-session--creds-scope-string scope)))
                      (unless (string-empty-p str) str)))
                  scopes))))

(defun safeslop-session--credential-count (data)
  "Return the count of display-safe credential scopes in session DATA."
  (length (safeslop-session--credential-scope-strings data)))

(defun safeslop-session--creds-summary (data)
  "Return DATA's value-free credential-scope summary, or an em dash when none.
Comma-joins every scope as \"kind name scope\"; old records without
`credential_scopes' (and empty/blank arrays) yield an em dash (specs/0086 T3)."
  (let ((rendered (safeslop-session--credential-scope-strings data)))
    (if rendered (string-join rendered ", ") "\u2014")))

(defun safeslop-session--project (data)
  "Return DATA's safe workspace basename, value-free, or nil.
Only the final path component is used so a private parent path never leaks
into a buffer name or header (specs/0086 value-free invariant).  The basename
is still filtered because workspace names are host-controlled text."
  (when-let* ((ws (safeslop-session--field data 'workspace)))
    (safeslop-session--safe-display-field
     (file-name-nondirectory (directory-file-name (expand-file-name ws))))))

(defun safeslop-session--buffer-label (session-id data)
  "Return a self-describing, value-free buffer label for SESSION-ID from DATA.
Shape follows specs/0071 rec #3:
`safeslop:<profile-or-name> <project> [env/net]'.  The profile wins over the
display name; the project is the workspace basename; tier/net is
`[environment/network]'.  With no legible identity/project data it falls back
to the legacy `safeslop-<id>' name so the buffer is still created and the
portal legacy lookup still finds it (specs/0086 T3)."
  (let* ((who (or (safeslop-session--safe-display-field
                   (safeslop-session--field data 'profile))
                  (safeslop-session--safe-display-field
                   (safeslop-session--field data 'name))))
         (project (safeslop-session--project data))
         (env (safeslop-session--safe-display-field
               (safeslop-session--field data 'environment)))
         (net (safeslop-session--safe-display-field
               (safeslop-session--field data 'network)))
         (tier (and env net (format "[%s/%s]" env net)))
         (parts (delq nil (list (and who (concat "safeslop:" who))
                                (and (null who) project (concat "safeslop:" project))
                                (and who project project)
                                (and (or who project) tier)))))
    (if parts
        (string-join parts " ")
      (concat "safeslop-"
              (or (safeslop-session--safe-display-field session-id) "unknown")))))

(defun safeslop-session--header-line (data)
  "Return the value-free header-line summary string for session DATA.
Restates profile/project/tier/net (the buffer label shape) and appends the
value-free credential-scope list as `creds: ...'
(or `creds: \u2014' for old records
and credential-less sessions).  Never includes token values, secret refs,
staged paths, or key refs (specs/0086 T3)."
  (let ((label (safeslop-session--buffer-label
                (or (safeslop-session--field data 'session_id) "") data)))
    (format "%s  creds: %s" label (safeslop-session--creds-summary data))))

(defun safeslop-session--fetch-data (session-id)
  "Return SESSION-ID's `session status' data alist, best effort, or nil.
Synchronous but best-effort: a failed or slow status must never block launching
the terminal, so any non-ok envelope simply yields nil and the caller falls back
to the legacy buffer name (specs/0086 T3)."
  (let ((env (safeslop--call-json
              (list "session" "status" "--session-id" session-id "--output" "json"))))
    (when (safeslop-contract-ok-p env)
      (safeslop-contract-data env))))

(defconst safeslop-session--doctor-args '("doctor" "--json")
  "Exact argv for the runtime-helper preflight doctor probe.")

(defun safeslop-session--doctor-tool-row (envelope tool)
  "Return TOOL's `doctor --json' row from ENVELOPE, or nil.
This is intentionally tolerant: failed doctor calls and old JSON without a
`data.tools' object carry no preflight signal, so launch continues and the CLI
remains authoritative."
  (when (and (safeslop-contract-ok-p envelope) (stringp tool))
    (let* ((data (safeslop-contract-data envelope))
           (tools (and (listp data) (alist-get 'tools data)))
           (row (and (listp tools) (alist-get (intern tool) tools))))
      (and (listp row) row))))

(defun safeslop-session--doctor-string-list (value)
  "Return VALUE's string elements as a list, ignoring malformed entries."
  (let ((items (cond
                ((vectorp value) (append value nil))
                ((listp value) value)
                (t nil))))
    (seq-filter #'stringp items)))

(defun safeslop-session--doctor-shadowed-docker (envelope)
  "Return docker shadow info from a doctor ENVELOPE, or nil when clean/old.
The returned alist has `path' for the selected helper and `shadowed_paths' for
lower-priority docker helpers reported by `tools.docker.shadowed_paths'."
  (when-let* ((row (safeslop-session--doctor-tool-row envelope "docker"))
              (shadowed (safeslop-session--doctor-string-list
                         (alist-get 'shadowed_paths row))))
    (let ((path (alist-get 'path row)))
      (list (cons 'path (and (stringp path) path))
            (cons 'shadowed_paths shadowed)))))

(defun safeslop-session--message-path (path)
  "Return PATH for a one-line diagnostic, suppressing control characters."
  (if (and (stringp path) (not (string-empty-p path)))
      (replace-regexp-in-string "[[:cntrl:]]+" "?" path)
    "<unknown>"))

(defun safeslop-session--docker-shadow-message (shadow)
  "Return an actionable, value-free user message for docker SHADOW info."
  (let* ((selected (safeslop-session--message-path (alist-get 'path shadow)))
         (shadowed (mapcar #'safeslop-session--message-path
                           (alist-get 'shadowed_paths shadow))))
    (format (concat "safeslop: docker helper is shadowed; selected path: %s; "
                    "shadowed paths: %s. Remove or reorder the shadowed docker "
                    "entries on PATH, then retry.")
            selected (string-join shadowed ", "))))

(defun safeslop-session--container-session-data-p (data)
  "Return non-nil when status DATA identifies a container session."
  (and (listp data)
       (equal (alist-get 'environment data) "container")))

(defun safeslop-session--run-runtime-preflight (data)
  "Run best-effort runtime preflight for container session DATA.
Only a positive `doctor --json' signal that docker has `shadowed_paths' aborts;
failed doctor calls and old JSON are pass-through UI misses, leaving the CLI to
enforce the authoritative launch policy."
  (when (safeslop-session--container-session-data-p data)
    (when-let* ((shadow (safeslop-session--doctor-shadowed-docker
                         (safeslop--call-json safeslop-session--doctor-args))))
      (user-error "%s" (safeslop-session--docker-shadow-message shadow))))
  data)

(defun safeslop-session--fetch-data-and-runtime-preflight (session-id)
  "Fetch SESSION-ID status, preflight container runtime shadows, and return data."
  (let ((data (safeslop-session--fetch-data session-id)))
    (safeslop-session--run-runtime-preflight data)
    data))

(defun safeslop-session--fetch-data-for-terminal (session-id preflight-runtime)
  "Fetch SESSION-ID status for terminal naming.
When PREFLIGHT-RUNTIME is non-nil, also preflight container runtime shadows.
Socket reattach does not need a runtime helper, so it passes nil and leaves any
attach failure to the CLI/socket path."
  (let ((data (safeslop-session--fetch-data session-id)))
    (when preflight-runtime
      (safeslop-session--run-runtime-preflight data))
    data))

(defun safeslop-session--make-terminal (name program argv)
  "Create terminal buffer *NAME* running PROGRAM with ARGV; return the
buffer. Prefer the eat terminal (pure-elisp, 24-bit color) when it can
be loaded, advertising TERM=xterm-256color; otherwise fall back to the
built-in term-mode. eat is an OPTIONAL dependency: with it absent (e.g.
CI) the agent still runs under term-mode, so every caller and test of
the term path is unaffected."
  (if (and (require 'eat nil t) (fboundp 'eat-exec))
      (let ((buf (get-buffer-create (concat "*" name "*"))))
        (with-current-buffer buf
          (eat-mode)
          ;; Bind dynamically so eat advertises a universally-understood truecolor
          ;; terminal even if the user never set `eat-term-name' themselves.
          (let ((eat-term-name "xterm-256color"))
            (eat-exec buf name program nil argv)))
        buf)
    (let ((buf (apply #'make-term name program nil argv)))
      (with-current-buffer buf
        (term-mode)
        (term-char-mode))
      buf)))

(defun safeslop-session--launch-term (session-id argv &optional preflight-runtime)
  "Launch ARGV for SESSION-ID under a terminal PTY; return the buffer.
Uses the eat terminal (24-bit truecolor) when available, else the built-in
term-mode (see `safeslop-session--make-terminal').  If PREFLIGHT-RUNTIME is
non-nil, preflight a known container record for shadowed docker before the
terminal subprocess starts.  If the process reports the PTY_UNAVAILABLE contract
error (no usable controlling terminal), switch to the read-only JSONL status
fallback (`safeslop-session-status-fallback')."
  ;; Fetch the session record best-effort BEFORE naming so the buffer is
  ;; self-describing from the first frame (specs/0086 T3).  Coupled run preflights
  ;; known container records before spawning; socket reattach deliberately does
  ;; not, because it rejoins an existing supervisor and does not execute docker.
  ;; A status miss yields nil and falls back to the legacy `safeslop-<id>' name;
  ;; failed/old doctor JSON also passes through so the CLI stays authoritative.
  (let* ((data (safeslop-session--fetch-data-for-terminal session-id preflight-runtime))
         (buf (safeslop-session--make-terminal
               (safeslop-session--buffer-label session-id data)
               safeslop-program argv)))
    ;; Set the buffer-local id AFTER terminal creation: the terminal major mode
    ;; runs `kill-all-local-variables', which would wipe an id set beforehand.
    ;; The header rides only on real record data, so a fallback-named legacy
    ;; launch (no data) stays header-less.
    (with-current-buffer buf
      (setq-local safeslop-session-id session-id)
      (when data
        (setq header-line-format (safeslop-session--header-line data))
        (safeslop-session--install-safety-chrome data)))
    (let ((proc (get-buffer-process buf)))
      (when proc
        ;; Capture raw stdout ahead of term's renderer, then key on it when the
        ;; process exits.  add-function (not set-process-*) preserves term's own
        ;; filter/sentinel so the PTY keeps working on the happy path.
        (add-function :before (process-filter proc)
                      (lambda (_p string)
                        (when (buffer-live-p buf)
                          (with-current-buffer buf
                            (setq safeslop-session--run-output
                                  (concat (or safeslop-session--run-output "") string))))))
        (add-function :after (process-sentinel proc)
                      (lambda (p _event)
                        (unless (process-live-p p)
                          (unless (or (safeslop-session--maybe-status-fallback buf session-id)
                                      (zerop (process-exit-status p)))
                            (safeslop-session--report-terminal-failure session-id))))))
        ;; Backstop for a process that already exited before the sentinel was wired.
        (when (and proc (not (process-live-p proc)))
          (unless (or (safeslop-session--maybe-status-fallback buf session-id)
                      (zerop (process-exit-status proc)))
            (safeslop-session--report-terminal-failure session-id))))
    (pop-to-buffer buf)
    buf))

(defvar safeslop-session-status-fallback-mode-map
  (let ((map (make-sparse-keymap)))
    (define-key map (kbd "P") #'safeslop-portal)
    (define-key map (kbd "L") #'safeslop-debug-log)
    (define-key map (kbd "q") #'quit-window)
    map)
  "Keymap for JSONL session status fallback buffers.")

(defun safeslop-session-status-fallback (&optional session-id)
  "Open a read-only compilation buffer for SESSION-ID JSONL status fallback.
The monitor process is started with an exact argv list; no shell is used."
  (interactive (list (safeslop-session--read-id "Session id: ")))
  (let* ((buf (get-buffer-create "*safeslop session status jsonl*"))
         (argv (list safeslop-program "session" "status"
                     "--session-id" session-id "--output" "jsonl")))
    (with-current-buffer buf
      (let ((inhibit-read-only t))
        (erase-buffer)
        (compilation-mode)
        (use-local-map (make-composed-keymap safeslop-session-status-fallback-mode-map
                                             compilation-mode-map))))
    (make-process :name "safeslop-status-jsonl"
                  :buffer buf
                  :command argv
                  :connection-type 'pipe
                  :noquery t)
    (pop-to-buffer buf)
    buf))


(provide 'safeslop-session-terminal)
;;; safeslop-session-terminal.el ends here
