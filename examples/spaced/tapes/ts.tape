# spaced TypeScript CLI demo — cross-language parity showcase
# Requires: spaced-ts in PATH
# IMPORTANT: all 3 tapes run identical commands for parity comparison

Output ../media/ts.gif

Set Width 1200
Set Height 800
Set FontSize 14
Set Theme "Catppuccin Mocha"

Sleep 500ms

# 1. Help — command groups
Type "spaced-ts --help"
Enter
Sleep 2s

# 2. Help all — reveals management group
Type "spaced-ts --help-all"
Enter
Sleep 2s

# 3. Per-group help
Type "spaced-ts --help-management"
Enter
Sleep 2s

# 4. Launch with SetFlag tags + log + bus
Type "spaced-ts launch starman --tag crew --tag +cargo --dry-run"
Enter
Sleep 2s

# 5. Quiet mode — suppress INFO
Type "spaced-ts --quiet launch starman --dry-run"
Enter
Sleep 2s

# 6. Config + XDG paths
Type "spaced-ts config"
Enter
Sleep 2s

# 7. Alias create + list
Type "spaced-ts alias add ml 'mission list'"
Enter
Sleep 1s

Type "spaced-ts alias list"
Enter
Sleep 2s

# 8. Format JSON
Type "spaced-ts mission list --format json"
Enter
Sleep 2s

# 9. Toolspec validation
Type "spaced-ts toolspec"
Enter
Sleep 2s

# 10. Interactive wizard
Type "spaced-ts launch --interactive"
Enter
Sleep 3s

# 11. Daemon stop with bus event
Type "spaced-ts daemon stop funding-secured"
Enter
Sleep 2s

Sleep 1s
