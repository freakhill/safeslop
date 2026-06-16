# managed-by: agentic_tactical_boots/install-fish-tools

complete -c slop-sandboxctl -f
complete -c slop-sandboxctl -n '__fish_use_subcommand' -a 'help list tutorial docker docker-tools local slop-brew-vm github forgejo safe-npm slop-safe-uv pinning'
complete -c slop-sandboxctl -n '__fish_seen_subcommand_from tutorial' -a 'docker local slop-brew-vm github-keys forgejo-keys network-limiting file-sharing'
