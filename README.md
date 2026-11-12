# Spellbook

Subscribe to git repositories that hold AI tooling and keep Claude Code and
GitHub Copilot in sync with them.

A team keeps its skills and instructions in one repository. Everyone
subscribes once, and `spellbook sync` installs what is new, updates what
changed and removes what was deleted, for both tools at the same time.

> **Status:** early. Skills, instructions, team groups and the lock file
> work today. Agents, automatic sync and MCP servers are planned; see the
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
| Claude Code instructions | `~/.claude/rules/` | `.claude/rules/` |
| GitHub Copilot instructions | `~/.copilot/copilot-instructions.md`, `~/.copilot/instructions/` | `.github/copilot-instructions.md`, `.github/instructions/` |
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
                       [--only skills|instructions]...
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

Any git repository works as a registry.

- Every directory that contains a `SKILL.md` is a skill, named after its
  directory.
- Every Markdown file below `instructions/` is an instruction, named after
  the file.

```
ai-registry/
├── registry.toml                  # optional: groups
├── instructions/
│   ├── company-standards.md
│   └── terraform-style.md
└── skills/
    ├── code-review/SKILL.md
    └── terraform-review/SKILL.md
```

- `--path platform` subscribes to one subdirectory only.
- `--skill code-review` installs only the named skills.
- `--only skills` installs only that kind of resource.
- `--ref` follows a branch, or pins a tag or commit.

### Instructions

An instruction is standing guidance for the agent. Write it once; Spellbook
renders it for each tool.

```markdown
---
description: How we write Terraform
paths:
  - "infra/**/*.tf"
---

Pin provider versions. Never hard-code account IDs.
```

Without `paths` an instruction always applies. With `paths` it applies only
to matching files.

| | Claude Code | GitHub Copilot |
|---|---|---|
| Always applies | `.claude/rules/<name>.md` | a marked block in `copilot-instructions.md` |
| With `paths` | `.claude/rules/<name>.md` with `paths` | `instructions/<name>.instructions.md` with `applyTo` |

Claude Code loads every file in its rules directory, so Spellbook never
edits your `CLAUDE.md`. Copilot reads always-on instructions from one file
only, so there Spellbook keeps each instruction between two marker comments
and leaves everything outside them exactly as you wrote it:

```markdown
# My own notes

<!-- spellbook:begin acme/company-standards -->
...
<!-- spellbook:end acme/company-standards -->
```

Blocks are layered: the registry with the higher priority comes first, and
inside a registry an included group comes before the group that includes it.
That gives company guidance, then team guidance, in a stable order.

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
instructions = ["company-standards"]

[groups.platform]
description = "Platform team"
include = ["base"]
skills = ["terraform-review"]
instructions = ["terraform-style"]
```

- A subscriber who chooses no group gets `default_groups`. Without
  `default_groups`, or without a `registry.toml`, they get everything.
- `include` pulls in everything from another group.
- `spellbook group join acme platform` adds a group to the ones you already
  have, `group leave` removes one, and `group list acme` shows what exists.

### Name collisions

When two registries offer a skill or an instruction with the same name, the
registry with the higher `--priority` wins and Spellbook prints a warning.
To keep both, give a registry a prefix:

- `--prefix collisions` installs under `<registry>-<name>` only when the
  plain name is already taken.
- `--prefix all` always installs under `<registry>-<name>`.

Spellbook rewrites the `name` in the skill's frontmatter to match.

## Reviewing and pinning updates

Every sync records the commit of each registry in `spellbook.lock`.

```sh
spellbook diff              # what changed upstream since the last sync
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
# only = ["skills"]                # only these kinds: skills, instructions
```

## What Spellbook will and will not touch

Spellbook records everything it installs and only ever changes those paths.

- A skill or rule file you created yourself is never overwritten or deleted,
  even if a registry offers one with the same name.
- Something you edited after Spellbook installed it is left alone, with a
  warning. `--force` overwrites it.
- In a shared file such as `copilot-instructions.md`, only the text between
  Spellbook's markers changes.
- If a registry cannot be fetched, what it installed stays and the command
  exits with an error.

## Roadmap

1. Core and skills — done
2. `registry.toml` manifest, team groups, prefixes for collisions, lockfile, `diff` — done
3. Instructions, layered (company, team) and path-scoped — done
4. Agents and prompts
5. Automatic sync, `sync --check` for CI, `doctor`
6. MCP servers and hooks
7. Installation with Homebrew

## Development

```sh
go test ./...
go vet ./... && gofmt -l .
```
