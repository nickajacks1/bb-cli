# AGENTS.md

`bb` is a Bitbucket Cloud CLI in Go (`github.com/nickajacks1/bb-cli`) built on urfave/cli/v3 and the charmbracelet suite.

## Layout

- Root package `bb` is the library; `NewApp()` in `app.go` builds the command tree. `cmd/bb/main.go` is a thin entry point.
- One `cmd_<name>.go` file per subcommand. Tests live in package `bb`.
- Tools come from `mise`. Use `just build`, `just test` and `just vet`. `just vet` runs `go fix` and `gofumpt`, so it rewrites files.

## Config

Config lives in `<UserConfigDir>/bb/config.json` (see `config.go`).

Reading a config option that is unset must fail. The error should tell the user how to fix it:

- Usually, run `bb configure`.
- If the command can skip the option, name the argument that does so. For example, `bb clone` without a home directory should suggest passing a destination.

Use `requireConfig` and `(*Config).requireHome` rather than reading empty values. Fail before doing any network work.

## Testing

Tests use real `git` against local bare repos and an `httptest` fake API. Point `XDG_CONFIG_HOME` and `XDG_CACHE_HOME` at temp dirs so tests never touch the real config.
