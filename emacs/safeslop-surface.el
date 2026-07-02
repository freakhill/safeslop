;;; safeslop-surface.el --- Shared navigation for safeslop dashboards -*- lexical-binding: t; -*-

;; Copyright (C) 2026

;; Author: safeslop
;; Package-Requires: ((emacs "32.0"))
;; Keywords: tools, processes, ai

;;; Commentary:

;; The safeslop operator view is two sibling dashboard buffers — Sessions
;; (`safeslop-portal') and Profiles (`safeslop-profiles') — that share one
;; navigation model (specs/0052).  This file holds everything common to both
;; (specs/0062):
;;
;;   - the parent keymap binding the surface switch keys (P/F, [/], TAB) and
;;     the textual tab strip rendered atop each buffer, so the active surface
;;     is legible without colour (specs/0031 non-colour signalling);
;;   - the shared presentation cells: network posture and isolation-tier
;;     colouring with their honest help-echo notes, the tier/net legends, and
;;     the key-hint legend renderer;
;;   - the persistent state banners (error / empty / loading);
;;   - `safeslop-surface-render', the one dashboard render engine.  It owns
;;     the async fetch, the tabulated-list reprint, the header re-insert, and
;;     the window scroll/cursor preservation from specs/0061, so the
;;     cursor-jump fix lives in exactly one place.
;;
;; Each surface mode sets `safeslop-surface-mode-map' as its keymap parent and
;; drives its redraws through the engine with surface-specific row builders.

;;; Code:

(require 'seq)
(require 'cl-lib)
(require 'subr-x)
(require 'tabulated-list)
(require 'safeslop-contract)
(require 'safeslop-client)

(declare-function safeslop-portal "safeslop-portal" ())
(declare-function safeslop-profiles "safeslop-profiles" ())
(declare-function safeslop-doctor "safeslop" ())

(defconst safeslop-surface--order
  '((sessions "Sessions" "P" safeslop-portal)
    (profiles "Profiles" "F" safeslop-profiles))
  "Ordered surfaces: (SYMBOL LABEL KEY COMMAND).
KEY is the direct switch key shown in the tab strip (also bound in every surface
map).  Drives the tab strip and `[' / `]' / TAB cycling.  Keep in step with the
modes that set `safeslop-surface-mode-map' as their parent.")

(defun safeslop-surface--command (entry)
  "Return the surface command for an `safeslop-surface--order' ENTRY."
  (nth 3 entry))

(defun safeslop-surface--current-sym ()
  "Return the surface symbol for the current buffer's major mode, or nil."
  (cond ((derived-mode-p 'safeslop-portal-mode) 'sessions)
        ((derived-mode-p 'safeslop-profiles-mode) 'profiles)))

(defun safeslop-surface--tab-strip (active)
  "Return the `Sessions | Profiles' tab strip for ACTIVE surface.
Each label is preceded by its direct switch key (faced as a key binding) so the
way to change surface is legible in the strip itself, not hidden.  ACTIVE (a
surface symbol) is rendered bold via `mode-line-emphasis'; the others are
`link'-faced and clickable (mouse-1).  A trailing hint names the cycle keys.
Ends with a blank line separating it from the buffer's own shortcut legend."
  (concat
   (mapconcat
    (lambda (entry)
      (let* ((sym (car entry)) (label (cadr entry)) (key (nth 2 entry))
             (cmd (safeslop-surface--command entry))
             (switch (lambda () (interactive) (funcall cmd)))
             (keymap (let ((m (make-sparse-keymap)))
                       (define-key m [mouse-1] switch)
                       m)))
        (concat
         (propertize key 'face 'help-key-binding
                     'mouse-face 'highlight 'keymap keymap
                     'help-echo (format "Switch to the %s surface (%s)" label key))
         " "
         (if (eq sym active)
             (propertize label 'face 'mode-line-emphasis)
           (propertize label
                       'face 'link
                       'mouse-face 'highlight
                       'help-echo (format "Switch to the %s surface (%s)" label key)
                       'keymap keymap)))))
    safeslop-surface--order
    "  │  ")
   "   "
   (propertize "TAB" 'face 'help-key-binding) "/"
   (propertize "[" 'face 'help-key-binding)
   (propertize "]" 'face 'help-key-binding)
   " cycle surface"
   "\n\n"))

;; --- In-place re-render without losing the operator's place -----------------
;; safeslop's dashboards refresh by reprinting the tabulated list and re-inserting
;; the header block above it.  A naive reprint erases the buffer, which collapses
;; `window-point' to the top in every window showing the buffer that is not the
;; selected one (and drops the scroll position everywhere) — the "cursor randomly
;; jumps to the top" bug, which also makes the row action keys land on the header
;; and appear broken.  This mirrors the fix slopmaxx's console uses: snapshot each
;; showing window's start/point before the reprint and restore them after, so an
;; automatic or manual refresh never scrolls or jumps the cursor out from under
;; the operator.

(defun safeslop-surface--capture-views ()
  "Snapshot (WINDOW POINT START) for every window showing the current buffer.
Pass the result to `safeslop-surface--restore-views' after an in-place re-render."
  (mapcar (lambda (win) (list win (window-point win) (window-start win)))
          (get-buffer-window-list (current-buffer) nil t)))

(defun safeslop-surface--goto-id (id)
  "Move point to the tabulated-list row whose id is ID; return non-nil if found.
Shared by the dashboards so a keep-point refresh can re-find the operator's row
*after* the header is re-inserted (inserting the header at `point-min' otherwise
leaves a first-row point stranded on the header — part of the cursor-jump fix)."
  (when id
    (goto-char (point-min))
    (let (found)
      (while (and (not found) (not (eobp)))
        (if (equal (tabulated-list-get-id) id)
            (setq found t)
          (forward-line 1)))
      found)))

(defun safeslop-surface--goto-first-row ()
  "Move point past the header block to the first tabulated-list row."
  (goto-char (point-min))
  (while (and (not (tabulated-list-get-id)) (not (eobp)))
    (forward-line 1)))

(defun safeslop-surface--restore-views (views &optional point)
  "Restore scroll and cursor for VIEWS captured by `safeslop-surface--capture-views'.
POINT, when non-nil, is the buffer position every window's cursor is synced to
(e.g. the row `tabulated-list-print' just restored); otherwise each window keeps
its own captured point.  Positions are clamped so a now-shorter buffer cannot
error, and `set-window-start' is non-forcing so the cursor stays visible."
  (dolist (view views)
    (let ((win (nth 0 view)) (old-point (nth 1 view)) (start (nth 2 view)))
      (when (window-live-p win)
        (set-window-point win (min (or point old-point) (point-max)))
        (set-window-start win (min start (point-max)) t)))))

(defun safeslop-surface--step (delta)
  "Switch to the surface DELTA positions from the current one, wrapping around."
  (let* ((syms (mapcar #'car safeslop-surface--order))
         (n (length syms))
         (cur (or (safeslop-surface--current-sym) (car syms)))
         (idx (or (seq-position syms cur) 0))
         (target (nth (mod (+ idx delta) n) syms)))
    (funcall (safeslop-surface--command (assq target safeslop-surface--order)))))

(defun safeslop-surface-next ()
  "Switch to the next safeslop surface."
  (interactive)
  (safeslop-surface--step 1))

(defun safeslop-surface-prev ()
  "Switch to the previous safeslop surface."
  (interactive)
  (safeslop-surface--step -1))

;;; Shared cells, faces, and legends -------------------------------------------

(defface safeslop-net-deny '((t :inherit success))
  "Face for network=deny: default-deny egress (safe default)."
  :group 'safeslop)

(defface safeslop-net-allow '((t :inherit warning))
  "Face for network=allow: open outbound egress from the boundary."
  :group 'safeslop)

(defface safeslop-surface-error '((t :inherit error :weight bold))
  "Face for persistent safeslop surface error banners."
  :group 'safeslop)

(defface safeslop-surface-hint '((t :inherit shadow))
  "Face for persistent safeslop surface empty/loading guidance."
  :group 'safeslop)

(defun safeslop-surface--net-cell (net)
  "Return NET as a colour-redundant cell with honest egress help."
  (pcase net
    ("allow" (propertize "allow" 'face 'safeslop-net-allow
                          'help-echo "network=allow: open outbound egress from the agent boundary"))
    ("deny" (propertize "deny" 'face 'safeslop-net-deny
                         'help-echo "network=deny: default-deny egress (safe default)"))
    (_ (or net ""))))

;; --- Isolation-tier signalling (specs/0052 #5) -------------------------------
;; The Env column shows the environment name (host/container); we colour that
;; text by isolation strength so the honest danger ramp the old GUI drew as
;; chrome is preserved — colour reinforces the always-present word, it never
;; replaces it (specs/0031 non-colour danger channel).  Shared here because the
;; Sessions and Profiles surfaces render the same ramp (specs/0062).

(defface safeslop-tier-host '((t :inherit error))
  "Face for the `host' environment: no isolation boundary (most dangerous)."
  :group 'safeslop)
(defface safeslop-tier-container '((t :inherit success))
  "Face for the `container' environment: egress-allowlisted network control."
  :group 'safeslop)

(defconst safeslop-surface--env-tiers
  ;; Mirrors internal/engine/policy/policy.go EnvTier (tier label + honest note),
  ;; ordered host < container (least -> most isolated).  Keep in sync with
  ;; EnvTier; doctor's data.tiers carries the authoritative copy at runtime.
  '(("host"      safeslop-tier-host      "none"               "no isolation boundary — the agent runs as you, with your full account")
    ("container" safeslop-tier-container "egress-allowlisted" "container + default-deny per-domain egress allowlist: stops curl|sh + accidental beaconing, not exfil via an allowed domain"))
  "Per-environment (FACE TIER NOTE) used to colour and annotate the Env cell.")

(defun safeslop-surface--env-face (env)
  "Return the isolation-tier face for environment ENV, or `default' if unknown."
  (or (nth 1 (assoc env safeslop-surface--env-tiers))
      'default))

(defun safeslop-surface--env-cell (env)
  "Return ENV as a tier-coloured tabulated-list cell with its honest note as help-echo.
The text label is always present, so colour is a redundant reinforcement, not the
sole signal (specs/0031).  An unknown env renders plainly."
  (let* ((row (assoc env safeslop-surface--env-tiers)))
    (if row
        (propertize env
                    'face (nth 1 row)
                    'help-echo (format "%s — %s" (nth 2 row) (nth 3 row)))
      env)))

(defun safeslop-surface--tier-legend ()
  "Return a one-line isolation-tier ramp legend (host most dangerous -> container safest)."
  (concat
   "tiers: "
   (mapconcat (lambda (row)
                (propertize (concat (car row) "=" (nth 2 row)) 'face (nth 1 row)))
              safeslop-surface--env-tiers "  ")
   "\n\n"))

(defun safeslop-surface--net-legend ()
  "Return a one-line legend for network posture."
  (concat "net: "
          (safeslop-surface--net-cell "deny") "=guarded  "
          (safeslop-surface--net-cell "allow") "=open\n"))

(defun safeslop-surface--danger-summary (agent environment network)
  "Return a one-line isolation/network risk summary for a launch/run confirm.
Shared by the Profiles launch confirm and the portal run confirm (specs/0063
F4), so the same world-changing action carries the same safety affordance on
every surface."
  (let ((note (or (nth 3 (assoc environment safeslop-surface--env-tiers))
                  "unknown isolation"))
        (net (if (equal network "allow")
                 "network ALLOW (egress reachable)"
               "network deny (default-deny egress)")))
    (format "%s · %s · %s" agent note net)))

(defun safeslop-surface--legend (hints)
  "Render HINTS — an alist of (KEY . ACTION) strings — as one shortcut legend line.
Keys are faced as bindings; ends with a blank separator line."
  (concat (mapconcat (lambda (pair)
                       (concat (propertize (car pair) 'face 'help-key-binding)
                               " " (cdr pair)))
                     hints "  ")
          "\n\n"))

;;; Persistent state banners -----------------------------------------------------

(defun safeslop-surface--error-message (envelope &optional fallback)
  "Return ENVELOPE's first error message, or FALLBACK."
  (or (alist-get 'message (car (safeslop-contract-errors envelope)))
      fallback
      "unknown error"))

(defun safeslop-surface--error-banner (label message)
  "Return persistent error guidance for LABEL and MESSAGE."
  (concat (propertize (format "⚠ %s failed: %s" label message)
                      'face 'safeslop-surface-error)
          " · "
          (propertize "g" 'face 'help-key-binding) " retry  "
          (propertize "d" 'face 'help-key-binding) " doctor  "
          (propertize "E" 'face 'help-key-binding) " last error  "
          (propertize "L" 'face 'help-key-binding) " debug\n"))

(defun safeslop-surface--empty-state (noun new-key)
  "Return persistent empty-state guidance for NOUN, advertising NEW-KEY when non-nil."
  (concat (propertize (format "No %s yet" noun) 'face 'safeslop-surface-hint)
          (if new-key
              (format " — press %s to create one, or %s to refresh.\n"
                      (propertize new-key 'face 'help-key-binding)
                      (propertize "g" 'face 'help-key-binding))
            (format " — press %s to refresh or %s for doctor.\n"
                    (propertize "g" 'face 'help-key-binding)
                    (propertize "d" 'face 'help-key-binding)))))

(defun safeslop-surface--loading (noun)
  "Return a non-blocking loading banner for NOUN."
  (propertize (format "↻ checking %s… (Emacs stays responsive)\n" noun)
              'face 'safeslop-surface-hint))

;;; Output-buffer breadcrumbs ------------------------------------------------------

(defun safeslop-surface--infer (args)
  "Infer the active surface symbol from safeslop ARGS."
  (pcase args
    (`("session" . ,_) 'sessions)
    (`("profile" . ,_) 'profiles)
    (`("validate" . ,_) 'profiles)
    (_ nil)))

(defun safeslop-surface--breadcrumb-title (args)
  "Return a compact title for an output buffer produced by ARGS.
Flags and file paths (absolute or home-relative) are dropped so the title stays
a short verb phrase (\"validate\", \"session status\"), not a full command line."
  (let ((tokens nil))
    (dolist (arg args)
      (unless (or (string-prefix-p "--" arg)
                  (string-match-p "\\`[/~]" arg))
        (push arg tokens)))
    (string-join (seq-take (nreverse tokens) 2) " ")))

(defun safeslop-surface--breadcrumb (args)
  "Return a operator UI tab strip plus compact output title for ARGS."
  (let ((active (safeslop-surface--infer args))
        (title (safeslop-surface--breadcrumb-title args)))
    (concat (safeslop-surface--tab-strip active)
            (when (not (string-empty-p title))
              (format "▸ %s\n\n" title)))))

;;; The shared dashboard render engine ---------------------------------------------

(defvar-local safeslop-surface--refresh-in-flight nil
  "Non-nil while an async dashboard fetch is outstanding for this buffer.
Set and cleared by `safeslop-surface-render'.  Guards pollers (the portal
auto-refresh timer) from stacking a second fetch on top of one that has not
returned yet — the slow-CLI pile-up that made refreshes fight input.")

(cl-defun safeslop-surface-render
    (&key argv label noun entries-fn header-fn empty-fn keep-point then)
  "Fetch ARGV asynchronously, then re-render the current dashboard in place.
The one render engine behind the Sessions/Profiles surfaces: the fetch
runs in a subprocess and the redraw happens in its callback, so neither a manual
\\`g' nor an auto-refresh timer ever freezes Emacs (specs/0052 #7).

ENTRIES-FN is called in the surface buffer with the parsed envelope and must
return the new `tabulated-list-entries', doing any surface-specific bookkeeping
(caching rows by id, remembering the config path) as it goes.  HEADER-FN returns
the header block inserted above the rows.  On a failed envelope the engine echoes
and inserts a persistent `safeslop-surface--error-banner' for LABEL (the
human-readable name of the fetch, e.g. \"session list\"); on an empty result it
inserts EMPTY-FN's guidance instead, so an empty table is never mysterious.
NOUN (plural, e.g. \"sessions\") drives the loading hint painted immediately
into a still-empty buffer on first open.

With KEEP-POINT non-nil, the engine snapshots every showing window's
scroll+cursor BEFORE the reprint, re-finds the operator's row AFTER the header
is re-inserted, and restores each view — the specs/0061 cursor-jump fix.  THEN,
when given, is called with point in the redrawn buffer and controls point
instead (used to reveal a just-created row); otherwise a plain render lands on
the first row."
  (let ((buf (current-buffer)))
    ;; First open: paint the header + loading hint at once so the operator never
    ;; faces a blank buffer while the first fetch is in flight.
    (when (and noun (= (point-min) (point-max)))
      (let ((inhibit-read-only t))
        (save-excursion
          (insert (funcall header-fn) (safeslop-surface--loading noun)))))
    (setq safeslop-surface--refresh-in-flight t)
    (safeslop--call-json-async
     argv
     (lambda (envelope)
       (when (buffer-live-p buf)
         (with-current-buffer buf
           (setq safeslop-surface--refresh-in-flight nil)
           ;; Snapshot each showing window's scroll+cursor BEFORE the reprint so a
           ;; refresh in a non-selected window can't collapse point to the top or
           ;; drop the scroll position; remember which row the operator was on, to
           ;; re-find it AFTER the header is re-inserted (a first row would
           ;; otherwise strand point on the header).
           (let ((views (and keep-point (safeslop-surface--capture-views)))
                 (kept-id (and keep-point (tabulated-list-get-id)))
                 (error-message (unless (safeslop-contract-ok-p envelope)
                                  (safeslop-surface--error-message
                                   envelope (format "%s failed" label)))))
             (setq tabulated-list-entries (funcall entries-fn envelope))
             (when error-message
               (message "safeslop %s: %s" label error-message))
             (tabulated-list-print keep-point)
             (let ((inhibit-read-only t))
               (save-excursion
                 (goto-char (point-min))
                 (insert (funcall header-fn))
                 (cond
                  (error-message
                   (insert (safeslop-surface--error-banner label error-message)))
                  ((null tabulated-list-entries)
                   (insert (funcall empty-fn))))))
             (cond
              ;; THEN controls point (reveal a specific/just-created row); let
              ;; redisplay scroll naturally so the revealed row is shown.
              (then (funcall then))
              ;; Keep-point refresh: re-find the operator's row now the header is
              ;; in place, then restore each window's captured scroll + cursor.
              (keep-point
               (or (safeslop-surface--goto-id kept-id)
                   (safeslop-surface--goto-first-row))
               (safeslop-surface--restore-views views (point)))
              (t (safeslop-surface--goto-first-row))))))))))

(defvar safeslop-surface-mode-map
  (let ((map (make-sparse-keymap)))
    (define-key map (kbd "P") #'safeslop-portal)
    (define-key map (kbd "F") #'safeslop-profiles)
    (define-key map (kbd "[") #'safeslop-surface-prev)
    (define-key map (kbd "]") #'safeslop-surface-next)
    (define-key map (kbd "TAB") #'safeslop-surface-next)
    (define-key map (kbd "<backtab>") #'safeslop-surface-prev)
    (define-key map (kbd "d") #'safeslop-doctor)
    (define-key map (kbd "E") #'safeslop-show-last-error)
    (define-key map (kbd "L") #'safeslop-debug-log)
    (define-key map (kbd "?") #'describe-mode)
    (define-key map (kbd "q") #'quit-window)
    map)
  "Parent keymap shared by every safeslop dashboard surface.
Surface modes install it with `set-keymap-parent'; their own action keys take
precedence and these switch keys fall through.")

(provide 'safeslop-surface)
;;; safeslop-surface.el ends here
