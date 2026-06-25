;;; config.el --- Doom Emacs safeslop local development setup -*- lexical-binding: t; -*-

;; Run `make install` or `make install-emacs` from the safeslop worktree you want
;; Doom to exercise.  This stable path avoids coupling Doom to a changing git
;; worktree path.
(let ((safeslop-dev-dir (expand-file-name "~/.local/share/safeslop/emacs"))
      (safeslop-bin (expand-file-name "~/.local/bin/safeslop")))
  (when (file-directory-p safeslop-dev-dir)
    (add-to-list 'load-path safeslop-dev-dir)
    (when (file-executable-p safeslop-bin)
      (setq safeslop-program safeslop-bin))
    (require 'safeslop-doom)
    (safeslop-bind-default-keys)
    (safeslop-doom-bind-leader)
    (use-package! safeslop
      :commands (safeslop-doctor
                 safeslop-policy-check-file
                 safeslop-session-new
                 safeslop-session-list
                 safeslop-session-status
                 safeslop-session-stop))))
