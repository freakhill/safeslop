# managed-by: safeslop/install-fish-tools

complete -c slop-macos-sandbox -f
complete -c slop-macos-sandbox -n '__fish_use_subcommand' -a 'help run shell print-profile'
complete -c slop-macos-sandbox -n '__fish_seen_subcommand_from run shell print-profile' -l network-policy -xa 'strict-egress off'
complete -c slop-macos-sandbox -n '__fish_seen_subcommand_from run shell print-profile' -l path-scope -xa 'cwd repo-root'
complete -c slop-macos-sandbox -n '__fish_seen_subcommand_from run shell print-profile' -l repo-root-access
complete -c slop-macos-sandbox -n '__fish_seen_subcommand_from run shell print-profile' -l allow-read -r
complete -c slop-macos-sandbox -n '__fish_seen_subcommand_from run shell print-profile' -l allow-write -r
