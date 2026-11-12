# Spellbook

Spellbook is a CLI that subscribes to git repositories ("registries") and
copies the AI tooling they contain into the places Claude Code and GitHub
Copilot read it from. It is modelled on the profile registry of Granted.

The binary is `spellbook`. There is no alias or short form.

## Commands

```sh
go build ./cmd/spellbook      # build
go test ./...                 # all tests (needs git on PATH)
go test ./internal/cli -run TestProjectScope   # one test
go vet ./... && gofmt -l .    # must be clean before committing
```

## Layout

| Package | Responsibility |
|---|---|
| `cmd/spellbook` | `main`, nothing else |
| `internal/cli` | cobra commands, all user-facing output |
| `internal/config` | TOML config, the two scopes (home, project), validation |
| `internal/target` | where each tool reads its files (`claude`, `copilot`) |
| `internal/registry` | fetch a registry, discover resources in it |
| `internal/gitx` | shell out to git |
| `internal/syncer` | resolve what should be installed and apply it |
| `internal/state` | record of what Spellbook wrote |
| `internal/fsutil` | hash and copy directories |

## Rules that must hold

- **Only touch what the state file records.** A path that is not in
  `state.json` belongs to the user. Never overwrite or delete it, not even
  with `--force`.
- **Do not destroy local edits silently.** A managed resource whose content
  hash differs from the recorded one is skipped with a warning unless
  `--force` is given.
- **A registry that failed to fetch removes nothing.** Its installed
  resources stay, and the command exits non-zero.
- **Copies, never symlinks.** Project files must be real files so they can be
  committed and seen by GitHub workflows.
- **Project state is portable.** Paths in `.spellbook/state.json` are
  relative to the project root.
- **Config is TOML.** Unknown keys are an error.

## Scopes

- Home: `~/.config/spellbook/config.toml`, writes below the home directory.
- Project (`--project`): `spellbook.toml` in the project root, found by
  walking up from the working directory; writes into the repository.

## Testing

Tests in `internal/cli` run the real command against local git repositories
in a temporary home. Add a test there for every user-visible behaviour.
`SPELLBOOK_CONFIG_DIR` and `SPELLBOOK_CACHE_DIR` redirect config and cache.

## Roadmap

1. Core and skills (done)
2. Registry manifest `registry.toml`, team groups with `include`, prefixes
   for collisions, lockfile, `diff`
3. Instructions: managed blocks in `CLAUDE.md` and
   `copilot-instructions.md`, layering, path-scoped instruction files
4. Agents and prompts, with frontmatter translation per target
5. Automatic sync, `sync --check` for CI, `doctor`
6. MCP servers and hooks, allowed per registry
