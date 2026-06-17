# managed-by: safeslop/install-fish-tools

complete -c slop-brew-vm -f
complete -c slop-brew-vm -n '__fish_use_subcommand' -a 'help create-base init run shell install verify-network copy-in copy-out destroy tui'
complete -c slop-brew-vm -n '__fish_seen_subcommand_from run shell install' -l network-policy -xa 'strict-egress proxy-only off'
complete -c slop-brew-vm -n '__fish_seen_subcommand_from verify-network' -l allow-url -r
complete -c slop-brew-vm -n '__fish_seen_subcommand_from verify-network' -l block-url -r
