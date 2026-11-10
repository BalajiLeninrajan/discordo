# Repository guidelines

This is a personal fork of [ayn2op/discordo](https://github.com/ayn2op/discordo). `origin` is the fork and `upstream` is the original.

## Issues and PRs

- Open PRs against `BalajiLeninrajan/discordo`, never against `ayn2op/discordo`. `gh pr create` targets the parent of a fork by default, so pass `--repo BalajiLeninrajan/discordo --base main`.
- Never open an issue or PR on upstream, and never comment there.
- Rebase-merge PRs so `main` stays linear and easy to rebase onto `upstream/main`.

## Building and testing

- `go.mod` needs a newer Go than the system one. Prefix `go` commands with `GOTOOLCHAIN=auto`.
- Before merging, run what CI runs:

  ```sh
  go test -race ./...
  go test -race -tags no_spoof_tls_fingerprint,no_voice,no_syntax_highlighting ./...
  go vet ./...
  ```

- `docs/config.md` is generated from the config structs and their doc comments. After changing anything in `internal/config`, run `go generate ./internal/config` and commit the result. CI fails if it is stale.
- Install a local build with `go build -trimpath -ldflags=-s -o ~/.local/bin/discordo .`

## Conventions

- Commit subjects are `<package>: <lowercase summary>`, like `guildstree: add a mark-read key`. Use `all:` for repo-wide changes.
- Personal preferences that an existing config option covers, like keybinds and theme colors, go in `~/.config/discordo/config.toml`, which chezmoi manages, not in the code defaults.
