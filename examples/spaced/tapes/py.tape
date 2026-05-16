# spaced Python CLI demo — cross-language parity showcase
# Requires: spaced-py in PATH
# IMPORTANT: all 3 tapes run identical commands for parity comparison

Output ../media/py.gif

Set Width 1200
Set Height 800
Set FontSize 14
Set Theme "Catppuccin Mocha"

Sleep 500ms

# 1. Help — command groups
Type "spaced-py --help"
Enter
Sleep 2s

# 2. Help all — reveals management group
Type "spaced-py --help-all"
Enter
Sleep 2s

# 3. Per-group help
Type "spaced-py --help-management"
Enter
Sleep 2s

# 4. Launch with SetFlag tags + log + bus
Type "spaced-py launch starman --tag crew --tag +cargo --dry-run"
Enter
Sleep 2s

# 5. Quiet mode — suppress INFO
Type "spaced-py --quiet launch starman --dry-run"
Enter
Sleep 2s

# 6. Config + XDG paths
Type "spaced-py config"
Enter
Sleep 2s

# 7. Alias create + list
Type "spaced-py alias add ml 'mission list'"
Enter
Sleep 1s

Type "spaced-py alias list"
Enter
Sleep 2s

# 8. Format JSON
Type "spaced-py mission list --format json"
Enter
Sleep 2s

# 9. Toolspec validation
Type "spaced-py toolspec"
Enter
Sleep 2s

# 10. Interactive wizard
Type "spaced-py launch --interactive"
Enter
Sleep 3s

# 11. Daemon stop with bus event
Type "spaced-py daemon stop funding-secured"
Enter
Sleep 2s

Sleep 1s
