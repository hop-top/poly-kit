# completion-v1/scripts

## What it answers

What `<program> completion <shell>` writes, byte for byte, for the program names and shells `../scripts.json` lists. Wrong place for what the scripts get back from `__complete`; that is `../requests.json`.

## Use it when

- you port the `completion` command to an SDK: render each case and compare with its golden
- you bump cobra in Go: regenerate nothing by hand; a changed script fails the Go runner, and the new bytes land here with the port updated in the same change

## Quick start

```sh
go test ./go/console/cli/completion/ -run '^TestConformanceCompletionScripts$'
```

## Contract

Goldens are cobra v1.10.2's bash (V2), zsh, fish and powershell scripts as a Go kit root emits them. `<program>.<ext>` carries descriptions; `<program>.nodesc.<ext>` is `--no-descriptions`, whose only change is calling `__completeNoDesc`. Every file here except this README must be claimed by a case, and every case must have its file.

## See also

- [`../README.md`](../README.md)
