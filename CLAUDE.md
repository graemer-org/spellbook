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
| `internal/target` | where each tool reads its files and in which format; one file per tool |
| `internal/blocks` | marked blocks inside files the user also edits |
| `internal/registry` | fetch a registry, discover resources, read `registry.toml`, resolve groups |
| `internal/lock` | `spellbook.lock`, the commit each registry was synced at |
| `internal/gitx` | shell out to git |
| `internal/syncer` | resolve what should be installed and apply it; compare and verify |
| `internal/ojson` | edit JSON files while keeping the order of their keys |
| `internal/autosync` | throttle for the automatic sync, session hooks for both tools |
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
- **Frontmatter of agents and prompts is passed through.** Do not drop
  fields Spellbook does not know. Only the `claude:` and `copilot:` sections
  are interpreted, and `name` is set to the installed name.
- **Syncing twice changes nothing.** The second run must report everything
  as unchanged and must not rewrite any file.
- **Guarded kinds need permission.** MCP servers and hooks run programs, so
  a registry installs them only when its `allow` list names `mcp` or
  `hooks`. Anything added later that executes code must be guarded the same
  way.
- **Hooks use Claude Code's event names for both tools.** Copilot accepts
  them and then applies Claude Code's matcher rules and input format. A
  hook entry is Spellbook's when its command ends in `# spellbook:<id>`.
- **No hook runs twice.** In a project Copilot also reads
  `.claude/settings.json`, so a hook written there for Claude Code is not
  written to `.github/hooks/` as well.
- **Never write `~/.claude.json`.** Claude Code rewrites that file itself.
  User-level MCP servers are changed through `claude mcp add-json` and
  `claude mcp remove`; the file is only read. The tool may store an entry
  in a different form, which is why the state keeps `source` next to `hash`.
- **A subscriber should be able to point at any repository.** Published
  collections use Copilot's conventions (`x.instructions.md` with `applyTo`,
  `x.agent.md`, upper case names, a root `mcp.json`). Accept those forms
  where they map cleanly instead of asking authors to rename files.
- **An interrupted sync must heal.** A file that exists, is not in the state
  and is identical to what would be written is taken over. Only a file with
  different content counts as the user's.
- **`sync --auto` is silent and never fails.** A tool may feed a session
  hook's output to the model, and a broken sync must not block a session.
  It does its work in a detached copy of the program, because a session
  hook is ended with its session, and marks the run as started so a second
  session does not sync at the same time. The outcome goes to the status
  file that `autosync status` reads.
- **A user's settings file keeps its content and key order.** Only the
  SessionStart entries that run Spellbook are added or removed, and a file
  that is not valid JSON is not touched.
- **`diff` changes nothing.** It never writes to targets, state or lock.

## Scopes

- Home: `~/.config/spellbook/config.toml`, writes below the home directory.
- Project (`--project`): `spellbook.toml` in the project root, found by
  walking up from the working directory; writes into the repository.

## Adding a tool

A tool is one `target.Spec` in its own file in `internal/target`
(`claude.go` and `copilot.go` are the models), registered from `init`.
Nothing else needs to know its name.

- Leave a function nil when the tool has no such kind of resource, and
  return `false` when it has none in the given scope. The sync then skips
  those resources and reports how many, per kind.
- Check every path and format against the tool's current documentation
  before writing it down, and say in the file's comment what was verified.
- A new place type (a different file layout, a different merge) belongs in
  `internal/syncer`, with the same ownership rules as the existing ones.
- Session hooks for the automatic sync are in `internal/autosync`; a tool
  without one returns `ErrUnsupported` there.
- Add tests in `internal/cli` that sync a registry with every kind into the
  new tool, at home and in a project, and extend the tables in the README
  and on the website.

## Open source

The project is licensed under Apache-2.0 (`LICENSE`, `NOTICE`). The README
is written for three readers in this order: one person, a project, an
organization. `CONTRIBUTING.md` and `SECURITY.md` must stay in step with
the commands and the permission model. The logo is the animated
`assets/spellbook.svg`; it has to read well on a light and on a dark page.

The website is the single file `docs/index.html`, served by GitHub Pages
from the `docs` folder of `main` at https://graemer-org.github.io/spellbook/.
It has no build step and no external resources. When a command or its
output changes, update the examples on the page as well as the README.

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
4. Agents and prompts, with frontmatter translation per target (done)
5. Automatic sync, `sync --check` for CI, `doctor` (done)
6. MCP servers, allowed per registry (done)
7. Installation with Homebrew through a tap (done; this repository is the
   tap)
8. Hooks from registries (done)

## Workflow

Work happens directly on `main`.

Commit messages follow Conventional Commits, because release-please derives
the next version and the changelog from them: `feat:` (minor), `fix:`
(patch), `feat!:` or a `BREAKING CHANGE:` footer (breaking), and `docs:`,
`test:`, `refactor:`, `chore:`, `ci:` for changes that need no release.

## Releases

`.github/workflows/release.yml` runs release-please on every push to
`main`. It maintains a release pull request; merging it creates the tag and
the GitHub release, and the `publish` job then runs `scripts/release.sh`,
uploads the archives and commits `Formula/spellbook.rb` to `main`.

- Never create tags or releases by hand, and never edit `CHANGELOG.md`,
  `.release-please-manifest.json` or `Formula/spellbook.rb`: all three are
  written by the release workflow.
- Merging the release pull request is the user's decision.
