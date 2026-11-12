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
| `internal/target` | where each tool reads its files and in which format (`claude`, `copilot`) |
| `internal/blocks` | marked blocks inside files the user also edits |
| `internal/registry` | fetch a registry, discover resources, read `registry.toml`, resolve groups |
| `internal/lock` | `spellbook.lock`, the commit each registry was synced at |
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
- **Config is TOML.** Unknown keys are an error. In a registry's
  `registry.toml` they are only a warning, because a registry may describe
  resource kinds a newer Spellbook understands.
- **The installed name is what tools see.** A skill installed under a
  prefixed name gets its frontmatter `name` rewritten, and the state hash is
  the hash of the rewritten copy.
- **Never edit the user's `CLAUDE.md`.** Claude Code instructions are files
  in `.claude/rules/`. Only Copilot's always-on instructions need a shared
  file, and there only the text between `spellbook:begin` and
  `spellbook:end` markers may change. A block that is not in the state file
  is left alone.
- **Syncing twice changes nothing.** The second run must report everything
  as unchanged and must not rewrite any file.
- **`diff` changes nothing.** It never writes to targets, state or lock.

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
   for collisions, lockfile, `diff` (done)
3. Instructions: rule files for Claude Code, managed blocks in
   `copilot-instructions.md`, layering, path-scoped instruction files (done)
4. Agents and prompts, with frontmatter translation per target
5. Automatic sync, `sync --check` for CI, `doctor`
6. MCP servers and hooks, allowed per registry
7. Installation with Homebrew through a tap

## Workflow

Work happens directly on `main`.
