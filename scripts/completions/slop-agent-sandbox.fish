# managed-by: safeslop/install-fish-tools

complete -c slop-agent-sandbox -f
complete -c slop-agent-sandbox -n '__fish_use_subcommand' -a 'run shell up down tui help'
complete -c slop-agent-sandbox -n '__fish_seen_subcommand_from run shell up down' -l network-policy -xa 'strict-egress proxy-only off'
