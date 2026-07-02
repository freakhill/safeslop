;;; safeslop-session.el --- Session commands for safeslop -*- lexical-binding: t; -*-

;; Copyright (C) 2026

;; Author: safeslop
;; Package-Requires: ((emacs "32.0"))
;; Keywords: tools, processes, ai

;;; Commentary:

;; Session-facing Emacs commands.  Attach runs the agent under a terminal PTY: the
;; eat terminal (pure-elisp, 24-bit truecolor) when available, else the built-in
;; term-mode.  When the CLI reports the PTY_UNAVAILABLE contract error (no usable
;; controlling terminal), attach switches to the read-only JSONL status fallback so
;; the buffer that started the session stays useful (specs/0050 PR4).

;;; Code:

(require 'subr-x)
(require 'seq)
(require 'term)
(require 'safeslop-contract)
(require 'safeslop-client)
(require 'safeslop-surface)
(require 'safeslop-output)

;; The portal sits above this layer (it requires safeslop-session); the reveal
;; hook is called `fboundp'-guarded and the command only from keymaps, so these
;; upward references stay late-bound.
(declare-function safeslop-portal--reveal-session "safeslop-portal" (id))
(declare-function safeslop-portal "safeslop-portal" ())
;; Optional eat terminal API — loaded lazily in `safeslop-session--make-terminal'
;; only when eat is installed; declared here so the byte-compiler stays quiet.
(declare-function eat-mode "eat" ())
(declare-function eat-exec "eat" (buffer name command startfile switches))
(defvar eat-term-name)
;; Defined once `compilation-mode' runs in the JSONL status fallback buffer.
(defvar compilation-mode-map)

(defun safeslop-session--create-args (agent workspace &optional environment network)
  "Return exact argv for creating an ad-hoc session with AGENT in WORKSPACE.
ENVIRONMENT (host|container) and NETWORK (deny|allow) default to container/deny
when nil, because the engine requires an explicit environment (specs/0053).  An
explicit empty string omits that flag for legacy/test callers that need to assert
raw argv shape."
  (let ((environment (if (null environment) "container" environment))
        (network (if (null network) "deny" network)))
    (append
     (list "session" "create" "--agent" agent "--workspace" (expand-file-name workspace))
     (when (and (stringp environment) (not (string-empty-p environment)))
       (list "--environment" environment))
     (when (and (stringp network) (not (string-empty-p network)))
       (list "--network" network))
     (list "--output" "json"))))

(defun safeslop-session--create-profile-args (profile)
  "Return exact argv for creating a session from existing PROFILE."
  (list "session" "create" "--profile" profile "--output" "json"))

(defun safeslop-session--profile-names (data)
  "Return sorted profile names from `profile list' DATA."
  (sort (mapcar (lambda (entry) (symbol-name (car entry)))
                (alist-get 'profiles data))
        #'string<))

(defun safeslop-session--read-profile-choice ()
  "Prompt for a profile name, or return nil for ad-hoc creation."
  (let* ((env (safeslop--call-json '("profile" "list" "--output" "json")))
         (profiles (and (safeslop-contract-ok-p env)
                        (safeslop-session--profile-names (safeslop-contract-data env))))
         (ad-hoc "<ad hoc>"))
    (if profiles
        (let ((pick (completing-read "Profile: " (cons ad-hoc profiles) nil t nil nil ad-hoc)))
          (unless (equal pick ad-hoc) pick))
      (message "safeslop: no profiles available; using ad-hoc session prompts")
      nil)))

(defconst safeslop-session-progress-buffer-name "*safeslop session progress*"
  "Buffer name for profile-backed session creation progress.")

(defvar safeslop-progress-mode-map
  (let ((map (make-sparse-keymap)))
    (define-key map (kbd "P") #'safeslop-portal)
    (define-key map (kbd "L") #'safeslop-debug-log)
    (define-key map (kbd "q") #'quit-window)
    map)
  "Keymap for live safeslop progress buffers.")

(define-derived-mode safeslop-progress-mode text-mode "safeslop-progress"
  "Major mode for live safeslop progress output buffers.
Keys: P portal, L debug log, q quit."
  (setq-local truncate-lines t))

(defun safeslop-session--progress-buffer (args)
  "Create, initialize, display, and return the progress buffer for ARGS."
  (let ((buf (get-buffer-create safeslop-session-progress-buffer-name)))
    (with-current-buffer buf
      (let ((inhibit-read-only t))
        (erase-buffer)
        (safeslop-progress-mode)
        (insert (format "$ %s %s\n\n" safeslop-program (string-join args " ")))
        (insert "status: running\n\n")))
    (display-buffer buf)
    buf))

(defun safeslop-session--progress-finish (buf status &optional envelope)
  "Append final process STATUS to progress BUF when it is still live.
On failure, ENVELOPE's first error message (when available) is appended to the
status line so the reason is readable where the operator is already looking."
  (when (buffer-live-p buf)
    (with-current-buffer buf
      (let ((inhibit-read-only t))
        (goto-char (point-max))
        (unless (bolp) (insert "\n"))
        (insert (if (equal status 0)
                    (format "\nsafeslop: finished successfully (exit %s)\n" status)
                  (format "\nsafeslop: failed (exit %s)%s\n" status
                          (let ((msg (and envelope
                                          (safeslop-surface--error-message envelope nil))))
                            (if msg (concat " — " msg) "")))))))))

(defun safeslop-session--call-json-async-with-progress (args callback)
  "Run safeslop ARGS asynchronously with stderr mirrored to a progress buffer.
Stdout stays reserved for the final JSON contract envelope; stderr is user-visible
progress for slow lazy profile image builds.  A thin wrapper over
`safeslop--call-json-async' (which owns spawn/parse/degrade): this only displays
the progress buffer up front and appends the outcome — success, CLI failure, or a
spawn failure — once the call resolves, reading `safeslop--last-call-status' in
the callback (stable there; sentinels run one at a time).  CALLBACK receives the
parsed envelope, exactly like `safeslop--call-json-async'."
  (let ((progress-buf (safeslop-session--progress-buffer args)))
    (safeslop--call-json-async
     args
     (lambda (envelope)
       (safeslop-session--progress-finish
        progress-buf safeslop--last-call-status envelope)
       (funcall callback envelope))
     progress-buf)))

(defun safeslop-session--create-async (args progress-p callback)
  "Run session-create ARGS and pass the resulting envelope to CALLBACK.
PROGRESS-P enables the visible progress buffer path because container sessions may
need a slow lazy first image build."
  (if progress-p
      (safeslop-session--call-json-async-with-progress args callback)
    (safeslop--call-json-async args callback)))

(defun safeslop-session--handle-create-result (args callback envelope)
  "Render session-create ENVELOPE for ARGS, reveal it, and run CALLBACK."
  (safeslop--show-envelope-buffer "*safeslop session*" args envelope)
  (let ((id (and (safeslop-contract-ok-p envelope)
                 (alist-get 'session_id (safeslop-contract-data envelope)))))
    ;; Reveal the new session in a live portal (the create is async, so a plain
    ;; portal refresh would race it), and — only when driven interactively (no
    ;; test CALLBACK) — offer to open it right away so "created" leads straight
    ;; to an obvious access path (specs/0052 #3).
    (when id
      (when (fboundp 'safeslop-portal--reveal-session)
        (safeslop-portal--reveal-session id))
      (when (null callback)
        (safeslop-session--offer-open id))))
  (when callback (funcall callback envelope)))

;;;###autoload
(defun safeslop-session-new (&optional agent workspace callback environment network profile)
  "Create a safeslop session and show the JSON envelope.
Interactively, first offer an existing PROFILE from `safeslop profile list'; if a
profile is chosen, `session create --profile' creates the session from the stored
policy.  Choosing `<ad hoc>' falls back to AGENT/WORKSPACE/ENVIRONMENT/NETWORK
prompts.  Noninteractive callers can pass AGENT/WORKSPACE as before; nil
ENVIRONMENT/NETWORK default to container/deny, while explicit empty strings omit
those flags for compatibility tests.  CALLBACK, when given, receives the envelope."
  (interactive
   (let ((profile (safeslop-session--read-profile-choice)))
     (if profile
         (list nil nil nil nil nil profile)
       (list (completing-read "Agent: " '("claude" "pi" "fish" "zsh") nil t nil nil "claude")
             (read-directory-name "Workspace: " nil nil t)
             nil ; callback: interactive use shows the envelope buffer, no extra hook
             (completing-read "Environment: " '("container" "host") nil t nil nil "container")
             (completing-read "Network: " '("deny" "allow") nil t nil nil "deny")
             nil))))
  (let* ((profile-p (and (stringp profile) (not (string-empty-p profile))))
         (args (if profile-p
                   (safeslop-session--create-profile-args profile)
                 (safeslop-session--create-args (or agent "claude")
                                                (or workspace default-directory)
                                                environment network)))
         (progress-p (or profile-p (equal (or environment "container") "container"))))
    (safeslop-session--create-async
     args progress-p
     (lambda (envelope)
       (safeslop-session--handle-create-result args callback envelope)))))

;;;###autoload
(defun safeslop-session-new-from-profile (profile &optional callback)
  "Create a safeslop session from existing PROFILE.
This is the noninteractive/testable profile-picker bridge used by
`safeslop-session-new'."
  (interactive (list (safeslop-session--read-profile-choice)))
  (unless (and (stringp profile) (not (string-empty-p profile)))
    (user-error "No profile selected"))
  (let ((args (safeslop-session--create-profile-args profile)))
    (safeslop-session--create-async
     args t
     (lambda (envelope)
       (safeslop-session--handle-create-result args callback envelope)))))

(defun safeslop-session--offer-open (id)
  "Offer to open (attach) the freshly created session ID right now."
  (when (y-or-n-p (format "Open session %s now? " id))
    (safeslop-session-attach id)))

(defun safeslop-session--run-args (session-id)
  "Return exact argv for running SESSION-ID."
  (list "session" "run" "--session-id" session-id))

(defun safeslop-session--attach-args (session-id)
  "Return exact argv for reattaching to SESSION-ID's detached supervisor."
  (list "session" "attach" "--session-id" session-id))

(defun safeslop-session--run-detached-args (session-id)
  "Return exact argv for starting SESSION-ID under a detached supervisor."
  (list "session" "run" "--session-id" session-id "--detach"))

(defun safeslop-session--sessions (&optional envelope)
  "Return session alists from a `session list' ENVELOPE, or fetch synchronously."
  (let ((env (or envelope (safeslop--call-json '("session" "list" "--output" "json")))))
    (when (safeslop-contract-ok-p env)
      (alist-get 'sessions (safeslop-contract-data env)))))

(defun safeslop-session--session-id-candidates (&optional envelope)
  "Return session ids from a `session list' ENVELOPE, or fetch them synchronously."
  (mapcar (lambda (s) (alist-get 'session_id s))
          (safeslop-session--sessions envelope)))

(defun safeslop-session--annotate (sess)
  "Return the completion annotation for session alist SESS (specs/0063 F7).
Bare ids are opaque; annotate with name (when the record has one — specs/0065
forward-compat), agent, status, and workspace so the pick is informed."
  (let ((name (alist-get 'name sess))
        (agent (alist-get 'agent sess))
        (status (alist-get 'status sess))
        (ws (alist-get 'workspace sess)))
    (concat "  "
            (string-join
             (delq nil (list (and (stringp name) (not (string-empty-p name)) name)
                             agent status
                             (and (stringp ws) (not (string-empty-p ws))
                                  (abbreviate-file-name ws))))
             " · "))))

(defun safeslop-session--read-id (prompt)
  "Read a session id with annotated completion; free text is allowed."
  (let* ((sessions (safeslop-session--sessions))
         (by-id (mapcar (lambda (s) (cons (alist-get 'session_id s) s)) sessions))
         (completion-extra-properties
          (list :annotation-function
                (lambda (id)
                  (if-let* ((sess (cdr (assoc id by-id))))
                      (safeslop-session--annotate sess)
                    "")))))
    (completing-read prompt (mapcar #'car by-id) nil nil)))

(defvar-local safeslop-session--run-output nil
  "Raw stdout accumulated from the `session run' process for this buffer.
Captured before term-mode renders it, so PTY_UNAVAILABLE detection is immune to
terminal line wrapping and term's trailing status line.")

(defvar-local safeslop-session--fallback-done nil
  "Non-nil once this run buffer has switched to the JSONL status fallback.
Guards against the run process's filter and sentinel both triggering the switch.")

(defun safeslop-session--pty-unavailable-p (output)
  "Return non-nil if OUTPUT carries the PTY_UNAVAILABLE contract error code.
A token match on the stable error code, not a strict JSON parse: the run process
is interactive, so its stdout may carry agent banner text around the envelope and
a PTY translates newlines, either of which can defeat a whole-buffer parse."
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

(defun safeslop-session--make-terminal (name program argv)
  "Create terminal buffer *NAME* running PROGRAM with ARGV; return the buffer.
Prefer the eat terminal (pure-elisp, 24-bit color) when it can be loaded,
advertising TERM=xterm-256color; otherwise fall back to the built-in term-mode.
eat is an OPTIONAL dependency: with it absent (e.g. CI) the agent still runs under
term-mode, so every caller and test of the term path is unaffected."
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

(defun safeslop-session--launch-term (session-id argv)
  "Launch ARGV for SESSION-ID under a terminal PTY; return the buffer.
Uses the eat terminal (24-bit truecolor) when available, else the built-in
term-mode (see `safeslop-session--make-terminal').  If the process reports the
PTY_UNAVAILABLE contract error (no usable controlling terminal), switch to the
read-only JSONL status fallback (`safeslop-session-status-fallback')."
  (let ((buf (safeslop-session--make-terminal
              (concat "safeslop-" session-id) safeslop-program argv)))
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
                          (safeslop-session--maybe-status-fallback buf session-id))))
        ;; Backstop for a process that already exited before the sentinel was wired.
        (unless (process-live-p proc)
          (safeslop-session--maybe-status-fallback buf session-id))))
    (pop-to-buffer buf)
    buf))

;;;###autoload
(defun safeslop-session-attach (&optional session-id)
  "Attach to SESSION-ID by running its agent under a built-in term-mode PTY.
On PTY_UNAVAILABLE (no usable controlling terminal), switch to the read-only
JSONL status fallback (`safeslop-session-status-fallback')."
  (interactive (list (safeslop-session--read-id "Run session: ")))
  (safeslop-session--launch-term session-id (safeslop-session--run-args session-id)))

;;;###autoload
(defun safeslop-session-run-detached (&optional session-id callback quiet)
  "Start SESSION-ID under a detached supervisor, asynchronously.
When QUIET is non-nil, do not pop the JSON envelope buffer; this is used by the
portal so row actions refresh in place instead of stealing the operator window."
  (interactive (list (safeslop-session--read-id "Run detached: ") nil nil))
  (let ((args (safeslop-session--run-detached-args session-id)))
    (safeslop--call-json-async
     args
     (lambda (envelope)
       (unless quiet
         (safeslop--show-envelope-buffer "*safeslop session detach*" args envelope))
       (when (and (null callback) (safeslop-contract-ok-p envelope)
                  (y-or-n-p (format "Detached. Reattach to %s now? " session-id)))
         (safeslop-session-reattach session-id))
       (when callback (funcall callback envelope))))))

;;;###autoload
(defun safeslop-session-list (&optional callback)
  "List safeslop sessions via the JSON contract, asynchronously.
CALLBACK, when given, is called with the envelope once it arrives (used by tests)."
  (interactive)
  (let ((args '("session" "list" "--output" "json")))
    (safeslop--call-json-async
     args
     (lambda (envelope)
       (safeslop--show-envelope-buffer "*safeslop sessions*" args envelope)
       (when callback (funcall callback envelope))))))

;;;###autoload
(defun safeslop-session-status (&optional session-id callback)
  "Show SESSION-ID status via the JSON contract, asynchronously.
CALLBACK, when given, is called with the envelope once it arrives (used by tests)."
  (interactive (list (safeslop-session--read-id "Session status: ")))
  (let ((args (list "session" "status" "--session-id" session-id "--output" "json")))
    (safeslop--call-json-async
     args
     (lambda (envelope)
       (safeslop--show-envelope-buffer "*safeslop session status*" args envelope)
       (when callback (funcall callback envelope))))))

;;;###autoload
(defun safeslop-session-stop (&optional session-id callback quiet)
  "Stop SESSION-ID, revoking credentials, asynchronously, and show the envelope.
Credential revocation can take a moment, so the call is async and never blocks
Emacs.  CALLBACK, when given, is called with the envelope once it arrives (tests).
When QUIET is non-nil, do not pop the JSON envelope buffer; this is used by the
portal so row actions refresh in place instead of stealing the operator window."
  (interactive
   (let ((id (safeslop-session--read-id "Stop session: ")))
     (unless (yes-or-no-p (format "Stop %s? This revokes staged credentials and tears down the boundary. " id))
       (user-error "Stop cancelled"))
     (list id nil)))
  (let ((args (list "session" "stop" "--session-id" session-id "--revoke-credentials" "--output" "json")))
    (safeslop--call-json-async
     args
     (lambda (envelope)
       (unless quiet
         (safeslop--show-envelope-buffer "*safeslop session stop*" args envelope))
       (when callback (funcall callback envelope))))))

(defun safeslop-session--remove-args (session-id)
  "Return exact argv for removing SESSION-ID's record."
  (list "session" "rm" "--session-id" session-id "--output" "json"))

(defun safeslop-session--prune-args ()
  "Return exact argv for pruning all stopped session records."
  (list "session" "prune" "--output" "json"))

(defun safeslop-session--rename-args (session-id name)
  "Return exact argv for renaming SESSION-ID to NAME (empty NAME clears it).
Name is a pure label (specs/0065 D1): the id stays the sole addressing
handle, so this only ever carries --session-id, never a name-as-selector."
  (list "session" "rename" "--session-id" session-id "--name" name
        "--output" "json"))

;;;###autoload
(defun safeslop-session-remove (&optional session-id callback quiet)
  "Remove SESSION-ID's record, asynchronously, and show the envelope.
This clears a stopped/created session out of the list (the portal exposes it as
`x').  The CLI refuses a running session and revokes any still-live staged
credentials before deleting the record.  CALLBACK, when given, receives the
envelope once it arrives (used by the portal to refresh, and by tests).  When
QUIET is non-nil, do not pop the JSON envelope buffer; this is used by the portal
so row actions refresh in place instead of stealing the operator window."
  (interactive (list (safeslop-session--read-id "Remove session: ") nil nil))
  (let ((args (safeslop-session--remove-args session-id)))
    (safeslop--call-json-async
     args
     (lambda (envelope)
       (unless quiet
         (safeslop--show-envelope-buffer "*safeslop session rm*" args envelope))
       (when callback (funcall callback envelope))))))

;;;###autoload
(defun safeslop-session-prune (&optional callback quiet)
  "Remove all stopped session records, asynchronously, and show the envelope.
Running and created sessions are left untouched; a crashed session (marked
running but whose process is gone) is reconciled to stopped and pruned in the
same pass.  CALLBACK, when given, receives the envelope once it arrives (used by
the portal to refresh, and by tests).  When QUIET is non-nil, do not pop the JSON
envelope buffer; this is used by the portal so row actions refresh in place
instead of stealing the operator window."
  (interactive)
  (let ((args (safeslop-session--prune-args)))
    (safeslop--call-json-async
     args
     (lambda (envelope)
       (unless quiet
         (safeslop--show-envelope-buffer "*safeslop session prune*" args envelope))
       (when callback (funcall callback envelope))))))

;;;###autoload
(defun safeslop-session-reattach (&optional session-id)
  "Reattach to SESSION-ID's detached supervisor over its socket, under a built-in
term-mode PTY.  Unlike `safeslop-session-attach' (which runs the agent coupled),
this rejoins an agent already running under a detached supervisor (specs/0051).
On PTY_UNAVAILABLE (no usable controlling terminal), switch to the read-only
JSONL status fallback (`safeslop-session-status-fallback')."
  (interactive (list (safeslop-session--read-id "Reattach session: ")))
  (safeslop-session--launch-term session-id (safeslop-session--attach-args session-id)))

(defun safeslop-session--name-for (session-id)
  "Return SESSION-ID's current display name, or an empty string when unset.
Used only to seed the rename prompt's default; a fresh `session list' is
cheap and keeps the default honest even when no portal cache is at hand."
  (let ((sess (seq-find (lambda (s) (equal (alist-get 'session_id s) session-id))
                        (safeslop-session--sessions))))
    (or (alist-get 'name sess) "")))

;;;###autoload
(defun safeslop-session-rename (&optional session-id name callback quiet)
  "Set SESSION-ID's display NAME, asynchronously, and show the envelope.
The name is a pure label (specs/0065): renaming touches nothing derived
from the id, so it works in any status and never becomes an addressing
handle.  Empty NAME clears the label.  CALLBACK, when given, receives the
envelope once it arrives (used by the portal to refresh, and by tests).
When QUIET is non-nil, do not pop the JSON envelope buffer; this is used by
the portal so row actions refresh in place instead of stealing the window."
  (interactive
   (let* ((id (safeslop-session--read-id "Rename session: "))
          (name (read-string (format "Name for %s (empty clears): " id)
                             (safeslop-session--name-for id))))
     (list id name nil nil)))
  (let ((args (safeslop-session--rename-args session-id (or name ""))))
    (safeslop--call-json-async
     args
     (lambda (envelope)
       (unless quiet
         (safeslop--show-envelope-buffer "*safeslop session rename*" args envelope))
       (when callback (funcall callback envelope))))))

(defun safeslop-session--detail-format (data)
  "Return a human-readable, faced detail view for session DATA."
  (cl-labels ((field (k) (let ((v (alist-get k data)))
                           (cond ((stringp v) v) ((null v) "") (t (format "%s" v)))))
              (line (k v &optional face)
                    (format "%-14s%s" k (if face (propertize (format "%s" v) 'face face) v))))
    (let* ((status (field 'status))
           (socket (field 'socket))
           (network (field 'network))
           (revoked (eq (alist-get 'credentials_revoked data) t))
           (last-error (field 'last_error))
           (detached (and (not (string-empty-p socket)) socket)))
      (mapconcat
       #'identity
       (delq nil
             (list (line "Session:" (field 'session_id))
                   ;; specs/0065: the optional display name rides right under the
                   ;; id it labels, shown only when the record actually has one.
                   (unless (string-empty-p (field 'name)) (line "Name:" (field 'name)))
                   (line "Agent:" (field 'agent))
                   (unless (string-empty-p (field 'profile)) (line "Profile:" (field 'profile)))
                   (line "Workspace:" (abbreviate-file-name (field 'workspace)))
                   ;; Same tier/net colour channel as the dashboards (specs/0063
                   ;; F11): the text label stays, colour + help-echo reinforce it.
                   (line "Environment:" (safeslop-surface--env-cell (field 'environment)))
                   (line "Network:" (safeslop-surface--net-cell network))
                   (line "Status:" status)
                   (line "Lifecycle:" (if detached "detached (survives buffer; reattach with A)"
                                         "coupled (tied to its terminal buffer)"))
                   (line "Credentials:" (if revoked "revoked" "live") (if revoked 'success 'warning))
                   (unless (string-empty-p (field 'pid)) (line "PID:" (field 'pid)))
                   (unless (string-empty-p (field 'exit_code)) (line "Exit code:" (field 'exit_code)))
                   (unless (string-empty-p last-error) (line "Last error:" last-error 'error))
                   (when detached (line "Socket:" socket))
                   ""
                   (pcase status
                     ("created" "Next: RET/r run coupled · R detach · s stop/revoke · P portal")
                     ("running" (if detached
                                    "Next: RET/A join detached · s stop/revoke · P portal"
                                  "Next: RET focus terminal · s stop/revoke · P portal"))
                     (_ "Next: c new session · P portal"))))
       "\n"))))

;;;###autoload
(defun safeslop-session-detail (&optional session-id data)
  "Show a read-only detail buffer for SESSION-ID using DATA or `session status'."
  (interactive (list (safeslop-session--read-id "Session detail: ")))
  (if data
      (let ((buf (get-buffer-create (format "*safeslop session %s*" session-id))))
        (with-current-buffer buf
          (safeslop-output-mode)
          (setq safeslop-output--args (list "session" "status" "--session-id" session-id "--output" "json")
                safeslop-output--buffer-name (buffer-name))
          ;; Refresh (raw `g' / Evil `gr') re-renders this faced detail view;
          ;; the generic output path would degrade it to the raw envelope dump
          ;; (specs/0063 F5).
          (setq safeslop-output--rerender
                (lambda (env)
                  (safeslop-session-detail session-id (safeslop-contract-data env))))
          (let ((inhibit-read-only t))
            (erase-buffer)
            (insert (safeslop-surface--breadcrumb safeslop-output--args))
            (insert (safeslop-session--detail-format data))
            (goto-char (point-min))))
        (pop-to-buffer buf)
        buf)
    (safeslop-session-status session-id)))

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

(provide 'safeslop-session)
;;; safeslop-session.el ends here
