# managed-by: safeslop/install-fish-tools

complete -c slop-forgejo-key -f
complete -c slop-forgejo-key -n '__fish_use_subcommand' -a 'instance-set bootstrap-config instance-list instance-remove create create-pair list revoke revoke-by-title revoke-expired print-ssh-config install-ssh-config uninstall-ssh-config here tui help'
complete -c slop-forgejo-key -n '__fish_seen_subcommand_from here' -a 'create-pair list revoke cleanup revoke-all'
complete -c slop-forgejo-key -n '__fish_seen_subcommand_from here' -l no-install-config
complete -c slop-forgejo-key -n '__fish_seen_subcommand_from here' -l ttl -r
complete -c slop-forgejo-key -n '__fish_seen_subcommand_from here' -l name -r
complete -c slop-forgejo-key -n '__fish_seen_subcommand_from instance-set instance-remove create create-pair list revoke revoke-by-title revoke-expired' -l instance -r
complete -c slop-forgejo-key -n '__fish_seen_subcommand_from create create-pair list revoke revoke-by-title revoke-expired install-ssh-config uninstall-ssh-config' -l repo -r
complete -c slop-forgejo-key -n '__fish_seen_subcommand_from create create-pair' -l access -xa 'ro rw'
complete -c slop-forgejo-key -n '__fish_seen_subcommand_from create create-pair' -l ttl -r
complete -c slop-forgejo-key -n '__fish_seen_subcommand_from create create-pair install-ssh-config uninstall-ssh-config instance-set instance-remove' -l name -r
complete -c slop-forgejo-key -n '__fish_seen_subcommand_from create-pair install-ssh-config' -l install-ssh-config
complete -c slop-forgejo-key -n '__fish_seen_subcommand_from instance-set' -l url -r
complete -c slop-forgejo-key -n '__fish_seen_subcommand_from instance-set' -l token-env -r
complete -c slop-forgejo-key -n '__fish_seen_subcommand_from create create-pair list revoke revoke-by-title revoke-expired install-ssh-config uninstall-ssh-config' -l forgejo-url -r
complete -c slop-forgejo-key -n '__fish_seen_subcommand_from print-ssh-config install-ssh-config' -l ro-key -r
complete -c slop-forgejo-key -n '__fish_seen_subcommand_from print-ssh-config install-ssh-config' -l rw-key -r
complete -c slop-forgejo-key -n '__fish_seen_subcommand_from create-pair install-ssh-config print-ssh-config' -l host-prefix -r
complete -c slop-forgejo-key -n '__fish_seen_subcommand_from revoke' -l id -r
complete -c slop-forgejo-key -n '__fish_seen_subcommand_from revoke-by-title' -l match -r
complete -c slop-forgejo-key -n '__fish_seen_subcommand_from uninstall-ssh-config' -l marker -r
complete -c slop-forgejo-key -n '__fish_seen_subcommand_from revoke revoke-by-title revoke-expired uninstall-ssh-config' -l yes
complete -c slop-forgejo-key -n '__fish_seen_subcommand_from bootstrap-config' -l force
