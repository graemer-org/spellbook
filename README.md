# Spellbook

Subscribe to git repositories that hold AI tooling and keep Claude Code and
GitHub Copilot in sync with them.

A team keeps its skills in one repository. Everyone subscribes once, and
`spellbook sync` installs new skills, updates changed ones and removes the
ones that were deleted, for both tools at the same time.

> **Status:** early. Skills, team groups and the lock file work today.
> Instructions, agents, automatic sync and MCP servers are planned; see the
> roadmap below.

## Install

```sh
go install github.com/graemer-org/spellbook/cmd/spellbook@latest
```

Spellbook needs `git` on the `PATH`.

## Quick start

```sh
spellbook init
spellbook registry add acme git@github.com:acme/ai-registry.git
spellbook list
```

After that, `spellbook sync` picks up whatever changed upstream.

## Home and project

Spellbook works on two levels. They are independent, and you can use both.

| | Home | Project (`--project`) |
|---|---|---|
| Config | `~/.config/spellbook/config.toml` | `spellbook.toml` in the project root |
| Claude Code skills | `~/.claude/skills/` | `.claude/skills/` |
| GitHub Copilot skills | `~/.copilot/skills/` | `.github/skills/` |
| State | `~/.config/spellbook/state.json` | `.spellbook/state.json` |
| Lock file | `~/.config/spellbook/spellbook.lock` | `spellbook.lock` |

In a project, Spellbook writes real copies into the repository. Commit them
together with `spellbook.toml`, `spellbook.lock` and `.spellbook/state.json`,
so GitHub workflows and teammates see the same files.

```sh
cd my-repo
spellbook init --project
spellbook registry add acme git@github.com:acme/ai-registry.git --project
git add spellbook.toml spellbook.lock .spellbook .claude .github
```

## Commands

```sh
spellbook init [--project] [--target claude] [--target copilot]

spellbook registry add <name> <git-url> [--ref main] [--path teams/platform]
                       [--group platform]... [--skill name]...
                       [--priority 10] [--prefix all|collisions] [--no-sync]
spellbook registry list
spellbook registry remove <name> [--force]
spellbook registry setup <directory>

spellbook group list <registry>
spellbook group join <registry> <group>...
spellbook group leave <registry> <group>...

spellbook sync [--dry-run] [--force] [--locked]
spellbook diff [registry] [--patch]
spellbook list
```

Every command takes `--project` (`-p`) to work on the project instead of the
home configuration.

## Registries

Any git repository works as a registry. Every directory that contains a
`SKILL.md` is a skill, named after its directory:

```
ai-registry/
├── base/
│   ├── code-review/SKILL.md
│   └── commit-style/SKILL.md
└── platform/
    └── terraform-review/SKILL.md
```

- `--path platform` subscribes to one subdirectory only.
- `--skill code-review` installs only the named skills.
- `--ref` follows a branch, or pins a tag or commit.

### Team groups

A registry can describe itself in a `registry.toml` at its root and sort its
skills into groups. `spellbook registry setup <directory>` creates a starting
point.

```toml
version = 1
default_groups = ["base"]

[groups.base]
description = "For everyone"
skills = ["code-review", "commit-style"]

[groups.platform]
description = "Platform team"
include = ["base"]
skills = ["terraform-review"]
```

- A subscriber who chooses no group gets `default_groups`. Without
  `default_groups`, or without a `registry.toml`, they get every skill.
- `include` pulls in everything from another group.
- `spellbook group join acme platform` adds a group to the ones you already
  have, `group leave` removes one, and `group list acme` shows what exists.

### Name collisions

When two registries offer a skill with the same name, the registry with the
higher `--priority` wins and Spellbook prints a warning. To keep both, give
a registry a prefix:

- `--prefix collisions` installs a skill as `<registry>-<skill>` only when
  the plain name is already taken.
- `--prefix all` always installs under `<registry>-<skill>`.

Spellbook rewrites the `name` in the skill's frontmatter to match.

## Reviewing and pinning updates

Every sync records the commit of each registry in `spellbook.lock`.

```sh
spellbook diff              # which skills changed upstream since the last sync
spellbook diff acme --patch # with the textual difference
spellbook sync              # move to the newest commits and update the lock
spellbook sync --locked     # install exactly what the lock records
```

`sync --locked` is for teammates and CI: it reproduces the committed state
and fails for a registry that is not in the lock file.

## Configuration

```toml
[targets.claude]
enabled = true

[targets.copilot]
enabled = true
skills_path = "~/.agents/skills"   # default: ~/.copilot/skills

[[registry]]
name = "acme"
url = "git@github.com:acme/ai-registry.git"
ref = "main"
priority = 10
groups = ["platform"]
prefix = "collisions"              # or "all"
# path = "teams/platform"          # only this subdirectory
# skills = ["terraform-review"]    # only these skills
```

## What Spellbook will and will not touch

Spellbook records everything it installs and only ever changes those paths.

- A skill you created yourself is never overwritten or deleted, even if a
  registry offers one with the same name.
- A skill you edited after Spellbook installed it is left alone, with a
  warning. `--force` overwrites it.
- If a registry cannot be fetched, its skills stay installed and the command
  exits with an error.

## Roadmap

1. Core and skills — done
2. `registry.toml` manifest, team groups, prefixes for collisions, lockfile, `diff` — done
3. Instructions, layered (company, team, project) and path-scoped
4. Agents and prompts
5. Automatic sync, `sync --check` for CI, `doctor`
6. MCP servers and hooks

## Development

```sh
go test ./...
go vet ./... && gofmt -l .
```
