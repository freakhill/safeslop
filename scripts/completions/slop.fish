# managed-by: safeslop/install-fish-tools

complete -c slop -f
complete -c slop -n '__fish_use_subcommand' -a 'help'
complete -c slop -l help -d 'Show help and exit'
complete -c slop -l version -d 'Print version and exit'
