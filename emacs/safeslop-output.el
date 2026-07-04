;;; safeslop-output.el --- Envelope output buffers for safeslop -*- lexical-binding: t; -*-

;; Copyright (C) 2026

;; Author: safeslop
;; Package-Requires: ((emacs "32.0"))
;; Keywords: tools, processes, ai

;;; Commentary:

;; Read-only rendering of safeslop JSON-contract envelopes (specs/0062):
;; `safeslop-output-mode', the generic envelope buffer renderer the discrete
;; commands share, and the safe `g' re-run that only replays read-only argv.
;; The subprocess substrate lives in safeslop-client; shared dashboard chrome
;; (tab strip, breadcrumbs) in safeslop-surface.

;;; Code:

(require 'subr-x)
(require 'safeslop-contract)
(require 'safeslop-client)
(require 'safeslop-surface)

(defvar-local safeslop-output--args nil
  "Safeslop argv that produced this output buffer, for safe refresh.")

(defvar-local safeslop-output--buffer-name nil
  "Name to reuse when refreshing this output buffer.")

(defvar-local safeslop-output--rerender nil
  "When non-nil, a function taking the parsed envelope that re-renders this buffer.
Detail and inspect buffers set it so a refresh (raw \\`g' or Evil \\`gr')
re-draws their faced view instead of degrading to the raw envelope dump
(specs/0063 F5).  Only consulted for a successful envelope; failures fall back
to the generic dump, which renders the error fields.")

(defun safeslop-output-refresh ()
  "Refresh this output buffer when its original command is read-only."
  (interactive)
  (if (safeslop--safe-rerun-p safeslop-output--args)
      (let ((args safeslop-output--args)
            (name (or safeslop-output--buffer-name (buffer-name)))
            (rerender safeslop-output--rerender))
        (safeslop--call-json-async
         args
         (lambda (env)
           (if (and rerender (safeslop-contract-ok-p env))
               (funcall rerender env)
             (safeslop--show-envelope-buffer name args env)))))
    (message "safeslop: this result came from a mutating command; rerun it from its surface so confirmation runs.")))

(defvar safeslop-output-mode-map
  (let ((map (make-sparse-keymap)))
    (define-key map (kbd "g") #'safeslop-output-refresh)
    (define-key map (kbd "e") #'safeslop-show-last-error)
    (set-keymap-parent map (make-composed-keymap safeslop-surface-mode-map special-mode-map))
    map)
  "Keymap for `safeslop-output-mode'.")

(define-derived-mode safeslop-output-mode special-mode "safeslop"
  "Major mode for read-only safeslop command output buffers."
  (setq-local truncate-lines t))

(defun safeslop--scalar (v)
  "Render a parsed JSON scalar V as a display string.
Matches the contract parser's :json-false/:json-null sentinels."
  (cond ((eq v t) "true")
        ((memq v '(:false :json-false)) "false")
        ((memq v '(:null :json-null)) "null")
        ((stringp v) v)
        ((numberp v) (number-to-string v))
        (t (format "%S" v))))

(defun safeslop--alist-p (x)
  "Return non-nil when X is a parsed JSON object (a symbol-keyed alist)."
  (and (consp x) (consp (car x)) (symbolp (caar x))))

(defun safeslop--insert-data (data indent)
  "Insert parsed envelope DATA readably at point, indented by INDENT levels.
Handles JSON objects (alists), arrays (lists), and scalars."
  (let ((pad (make-string (* 2 indent) ?\s)))
    (cond
     ((safeslop--alist-p data)
      (dolist (kv data)
        (let ((k (car kv)) (v (cdr kv)))
          (cond
           ((safeslop--alist-p v)
            (insert (format "%s%s:\n" pad k))
            (safeslop--insert-data v (1+ indent)))
           ((and (consp v) (safeslop--alist-p (car v)))
            (insert (format "%s%s:\n" pad k))
            (safeslop--insert-data v (1+ indent)))
           ((and (consp v) (not (safeslop--alist-p v)))
            (insert (format "%s%s: %s\n" pad k
                            (mapconcat #'safeslop--scalar v ", "))))
           (t (insert (format "%s%s: %s\n" pad k (safeslop--scalar v))))))))
     ((consp data)
      (dolist (item data)
        (if (safeslop--alist-p item)
            (progn (insert (format "%s-\n" pad))
                   (safeslop--insert-data item (1+ indent)))
          (insert (format "%s- %s\n" pad (safeslop--scalar item))))))
     (t (insert (format "%s%s\n" pad (safeslop--scalar data)))))))

(defun safeslop--show-envelope-buffer (name args envelope)
  "Render ENVELOPE for safeslop ARGS into buffer NAME and return ENVELOPE."
  (let ((buf (get-buffer-create name)))
    (with-current-buffer buf
      (setq safeslop-output--args args
            safeslop-output--buffer-name name)
      (let ((inhibit-read-only t))
        (erase-buffer)
        (insert (safeslop-surface--breadcrumb args))
        (insert (format "$ %s %s\n\n" safeslop-program (string-join args " ")))
        (insert (format "ok: %s\n" (if (safeslop-contract-ok-p envelope) "true" "false")))
        (dolist (warning (safeslop-contract-warnings envelope))
          (insert (format "warning[%s]: %s\n"
                          (alist-get 'code warning)
                          (alist-get 'message warning))))
        (dolist (err (safeslop-contract-errors envelope))
          (insert (format "error[%s]: %s\n"
                          (alist-get 'code err)
                          (alist-get 'message err))))
        (let ((data (safeslop-contract-data envelope)))
          (when data
            (insert "\n")
            (safeslop--insert-data data 0)))
        (safeslop-output-mode)))
    (pop-to-buffer buf))
  envelope)

(provide 'safeslop-output)
;;; safeslop-output.el ends here
