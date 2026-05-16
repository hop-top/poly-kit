# spaced Go CLI demo — cross-language parity showcase
# Requires: spaced-go in PATH
# IMPORTANT: all 3 tapes run identical commands for parity comparison

Output ../media/go.gif

Set Width 1200
Set Height 800
Set FontSize 14
Set Theme "Catppuccin Mocha"

Sleep 500ms

# 1. Help — command groups
Type "spaced-go --help"
Enter
Sleep 2s

# 2. Help all — reveals management group
Type "spaced-go --help-all"
Enter
Sleep 2s

# 3. Per-group help
Type "spaced-go --help-management"
Enter
Sleep 2s

# 4. Launch with SetFlag tags + log + bus
Type "spaced-go launch starman --tag crew --tag +cargo --dry-run"
Enter
Sleep 2s

# 5. Quiet mode — suppress INFO
Type "spaced-go --quiet launch starman --dry-run"
Enter
Sleep 2s

# 6. Config + XDG paths
Type "spaced-go config"
Enter
Sleep 2s

# 7. Alias create + list
Type "spaced-go alias add ml 'mission list'"
Enter
Sleep 1s

Type "spaced-go alias list"
Enter
Sleep 2s

# 8. Format JSON
Type "spaced-go mission list --format json"
Enter
Sleep 2s

# 9. Toolspec validation
Type "spaced-go toolspec"
Enter
Sleep 2s

# 10. Interactive wizard
Type "spaced-go launch --interactive"
Enter
Sleep 3s

# 11. Daemon stop with bus event
Type "spaced-go daemon stop funding-secured"
Enter
Sleep 2s

Sleep 1s
