# Spellbook

Subscribe to git repositories that hold AI tooling and keep Claude Code and
GitHub Copilot in sync with them.

A team keeps its skills in one repository. Everyone subscribes once, and
`spellbook sync` installs new skills, updates changed ones and removes the
ones that were deleted, for both tools at the same time.

> **Status:** early. Skills work today. Team groups, instructions, agents,
> automatic sync and MCP servers are planned; see the roadmap below.

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

In a project, Spellbook writes real copies into the repository. Commit them
together with `spellbook.toml` and `.spellbook/state.json`, so GitHub
workflows and teammates see the same files.

```sh
cd my-repo
spellbook init --project
spellbook registry add acme git@github.com:acme/ai-registry.git --project
git add spellbook.toml .spellbook .claude .github
```

## Commands

```sh
spellbook init [--project] [--target claude] [--target copilot]
spellbook registry add <name> <git-url> [--ref main] [--path teams/platform]
                                        [--priority 10] [--skill name]... [--no-sync]
spellbook registry list
spellbook registry remove <name> [--force]
spellbook sync [--dry-run] [--force]
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

- `--path platform` subscribes to one subdirectory only, for example one
  team's folder.
- `--skill code-review` installs only the named skills.
- `--ref` follows a branch, or pins a tag or commit.
- When two registries offer a skill with the same name, the registry with
  the higher `--priority` wins and Spellbook prints a warning.

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
path = "platform"
priority = 10
skills = ["terraform-review"]
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
2. `registry.toml` manifest, team groups, prefixes for collisions, lockfile, `diff`
3. Instructions, layered (company, team, project) and path-scoped
4. Agents and prompts
5. Automatic sync, `sync --check` for CI, `doctor`
6. MCP servers and hooks

## Development

```sh
go test ./...
go vet ./... && gofmt -l .
```
