# managed-by: safeslop/install-fish-tools

complete -c slop-gh-key -f
complete -c slop-gh-key -n '__fish_use_subcommand' -a 'create create-pair print-ssh-config install-ssh-config uninstall-ssh-config list revoke revoke-by-title revoke-expired here tui help'
complete -c slop-gh-key -n '__fish_seen_subcommand_from here' -a 'create-pair list revoke cleanup revoke-all'
complete -c slop-gh-key -n '__fish_seen_subcommand_from here' -l no-install-config -d 'For here create-pair: skip ssh config install'
complete -c slop-gh-key -n '__fish_seen_subcommand_from here' -l ttl -r
complete -c slop-gh-key -n '__fish_seen_subcommand_from here' -l name -r
complete -c slop-gh-key -n '__fish_seen_subcommand_from create create-pair list revoke revoke-by-title revoke-expired install-ssh-config uninstall-ssh-config' -l repo -r
complete -c slop-gh-key -n '__fish_seen_subcommand_from create create-pair' -l access -xa 'ro rw'
complete -c slop-gh-key -n '__fish_seen_subcommand_from create create-pair' -l ttl -r
complete -c slop-gh-key -n '__fish_seen_subcommand_from create create-pair install-ssh-config uninstall-ssh-config' -l name -r
complete -c slop-gh-key -n '__fish_seen_subcommand_from create-pair install-ssh-config' -l install-ssh-config
complete -c slop-gh-key -n '__fish_seen_subcommand_from print-ssh-config install-ssh-config' -l ro-key -r
complete -c slop-gh-key -n '__fish_seen_subcommand_from print-ssh-config install-ssh-config' -l rw-key -r
complete -c slop-gh-key -n '__fish_seen_subcommand_from create-pair install-ssh-config print-ssh-config' -l host-prefix -r
complete -c slop-gh-key -n '__fish_seen_subcommand_from revoke' -l id -r
complete -c slop-gh-key -n '__fish_seen_subcommand_from revoke-by-title' -l match -r
complete -c slop-gh-key -n '__fish_seen_subcommand_from uninstall-ssh-config' -l marker -r
complete -c slop-gh-key -n '__fish_seen_subcommand_from revoke revoke-by-title revoke-expired uninstall-ssh-config' -l yes
