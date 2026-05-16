# Shell Completion User Guide

Enable tab completion for any kit-built CLI. Examples use
`spaced`; replace with your binary name.

---

## Bash

```sh
# One-time setup (persistent across sessions)
spaced completion bash \
  > ~/.local/share/bash-completion/completions/spaced

# Or source in .bashrc (regenerated each shell start)
echo 'eval "$(spaced completion bash)"' >> ~/.bashrc
```

## Zsh

```sh
# One-time setup
spaced completion zsh > "${fpath[1]}/_spaced"

# Or source in .zshrc
echo 'eval "$(spaced completion zsh)"' >> ~/.zshrc
```

May need `compinit` if not already loaded:

```sh
# Add to .zshrc before the eval line
autoload -Uz compinit && compinit
```

## Fish

```sh
spaced completion fish \
  > ~/.config/fish/completions/spaced.fish
```

## PowerShell

```powershell
spaced completion powershell >> $PROFILE
```

---

## Platform Notes

### macOS

**Homebrew bash** -- system bash is v3 (no dynamic completion).
Install bash-completion v2:

```sh
brew install bash-completion@2
```

Then write completions to homebrew's path:

```sh
spaced completion bash \
  > "$(brew --prefix)/etc/bash_completion.d/spaced"
```

**Homebrew zsh** -- completions go to:

```sh
spaced completion zsh \
  > "$(brew --prefix)/share/zsh/site-functions/_spaced"
```

**Default zsh on macOS** -- if not using homebrew zsh:

```sh
mkdir -p ~/.zsh/completions
spaced completion zsh > ~/.zsh/completions/_spaced
```

Add to `.zshrc`:

```sh
fpath=(~/.zsh/completions $fpath)
autoload -Uz compinit && compinit
```

### Linux

**System-wide** (requires root):

```sh
# bash
sudo spaced completion bash > /etc/bash_completion.d/spaced

# zsh
sudo spaced completion zsh \
  > /usr/share/zsh/site-functions/_spaced
```

**User-local** (no root needed):

```sh
mkdir -p ~/.local/share/bash-completion/completions
spaced completion bash \
  > ~/.local/share/bash-completion/completions/spaced
```

### Windows

- **PowerShell**: add to `$PROFILE` (see above)
- **WSL**: use Linux instructions

---

## Verifying It Works

Restart your shell (or source the config), then test:

```sh
spaced <TAB>                       # subcommands
spaced launch <TAB>                # mission names
spaced launch starman --orbit <TAB> # leo, geo, lunar, ...
```

If no suggestions appear:
1. Confirm the completion script exists at the expected path
2. Confirm `compinit` is loaded (zsh)
3. Confirm `bash-completion` package is installed (bash)
4. Try `spaced completion bash | head` -- should print a
   shell function, not an error

---

## How It Works

1. `spaced completion <shell>` generates a shell-specific
   script (bash/zsh/fish/powershell)
2. The script registers a callback that runs
   `spaced __complete <args...>` each time you press TAB
3. The CLI resolves the appropriate `Completer` from its
   registry (flag completers, positional arg completers)
4. Dynamic completers (mission names, config keys) execute
   at tab-press time -- results always reflect current state
5. The shell renders suggestions with optional descriptions
