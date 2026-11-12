<p align="center">
  <img src="assets/spellbook.svg" alt="Spellbook: shared spells for Claude Code and GitHub Copilot" width="760">
</p>

<p align="center">
  <a href="https://github.com/graemer-org/spellbook/actions/workflows/ci.yml"><img src="https://github.com/graemer-org/spellbook/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/graemer-org/spellbook/releases/latest"><img src="https://img.shields.io/github/v/release/graemer-org/spellbook" alt="Latest release"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue" alt="License: Apache-2.0"></a>
</p>

<p align="center">
  <a href="https://graemer-org.github.io/spellbook/"><b>Website</b></a> ·
  <a href="#use-it-for-yourself">For yourself</a> ·
  <a href="#use-it-in-a-project">In a project</a> ·
  <a href="#use-it-in-an-organization">In an organization</a>
</p>

Spellbook keeps your team's AI tooling in git and installs it into Claude
Code and GitHub Copilot.

Write a skill, an instruction, an agent, a prompt, an MCP server or a hook
once, in a git repository. Everyone who subscribes gets it in both tools,
and `spellbook sync` brings every later change: new things are installed,
changed things updated, deleted things removed.

```sh
brew tap graemer-org/spellbook https://github.com/graemer-org/spellbook
brew install spellbook

spellbook init
spellbook registry add acme git@github.com:acme/ai-registry.git
spellbook autosync enable
```

## What it syncs

| | What it is | Claude Code | GitHub Copilot |
|---|---|:-:|:-:|
| Skills | Instructions and scripts the agent loads when a task calls for them | ✓ | ✓ |
| Instructions | Standing guidance, always on or limited to certain files | ✓ | ✓ |
| Agents | Specialists with their own prompt, tools and model | ✓ | ✓ |
| Prompts | Reusable tasks you start yourself | ✓ | in projects |
| MCP servers | Connections to external tools and data | ✓ | ✓ |
| Hooks | Commands that run when something happens in a session | ✓ | ✓ |

Each one is written once in the registry and rendered in the format each
tool expects.

## Contents

- [Install](#install)
- [Use it for yourself](#use-it-for-yourself)
- [Use it in a project](#use-it-in-a-project)
- [Use it in an organization](#use-it-in-an-organization)
- [Writing a registry](#writing-a-registry)
- [Reference](#reference)
- [What Spellbook will and will not touch](#what-spellbook-will-and-will-not-touch)
- [Contributing](#contributing)

## Install

With Homebrew, on macOS and Linux:

```sh
brew tap graemer-org/spellbook https://github.com/graemer-org/spellbook
brew install spellbook
```

With Go:

```sh
go install github.com/graemer-org/spellbook/cmd/spellbook@latest
```

Or download an archive from the
[releases page](https://github.com/graemer-org/spellbook/releases).
Spellbook needs `git` on the `PATH`.

## Use it for yourself

You want the same skills and habits on every machine and in every project.

**1. Set up.** This creates `~/.config/spellbook/config.toml` and enables
both tools.

```sh
spellbook init
```

**2. Subscribe.** Any git repository that contains skills works, including
public collections. Take all of it or name what you want:

```sh
spellbook registry add anthropic https://github.com/anthropics/skills --skill pdf --skill docx
```

**3. Keep your own spells in a repository.** `registry setup` creates the
skeleton; push it wherever you keep your code and subscribe to it.

```sh
spellbook registry setup ~/code/my-spells
cd ~/code/my-spells && git init && git add -A && git commit -m "My spells"
# create an empty repository at your git host, then:
git remote add origin git@github.com:you/my-spells.git && git push -u origin main

spellbook registry add mine git@github.com:you/my-spells.git
```

From now on you edit a skill in that repository, push, and every machine
picks it up.

**4. Let it update on its own.**

```sh
spellbook autosync enable
```

Both tools then run a sync when a session starts, at most once a day, in
the background.

Useful along the way:

```sh
spellbook list      # what is installed, and from where
spellbook diff      # what changed upstream since the last sync
spellbook sync      # update now
spellbook doctor    # check the setup
```

At this level Spellbook writes below your home directory, for example
`~/.claude/skills/` and `~/.copilot/skills/`, so everything applies in every
project you open.

## Use it in a project

You want everyone who works on a repository, and every agent that runs
there, to get the same tooling, without anyone installing anything.

**1. Set up inside the repository.**

```sh
cd my-repo
spellbook init --project
spellbook registry add acme git@github.com:acme/ai-registry.git --project --group backend
```

Spellbook writes real files into the repository: `.claude/skills/`,
`.github/skills/`, `.claude/rules/`, `.github/copilot-instructions.md` and
so on.

**2. Commit them.**

```sh
git add spellbook.toml spellbook.lock .spellbook .claude .github
git commit -m "Add shared AI tooling"
```

Teammates get the tooling with `git pull`. They do not need Spellbook, and
neither do cloud agents or GitHub workflows, because the files are simply
part of the repository.

**3. Update deliberately.** Whoever maintains the setup reviews what changed
upstream and commits the result like any other change:

```sh
spellbook diff --project --patch   # what would change
spellbook sync --project           # apply it and update spellbook.lock
git add -A && git commit -m "Update shared AI tooling"
```

**4. Guard it in CI.** `sync --check` changes nothing and fails when the
committed files differ from what `spellbook.lock` describes, for example
because someone edited a synced file by hand.

```yaml
- run: spellbook sync --project --check
```

`spellbook sync --project --locked` reproduces exactly the locked state,
which is what you want on a fresh checkout or after resolving a conflict.

Home and project are independent. You can have your personal spells at home
and a project's own set in the repository at the same time.

## Use it in an organization

You want company standards for everyone, team tooling for each team, and a
way to change both that is reviewed like code.

### One registry, groups per team

Create a registry repository and sort its content into groups. A group can
include another, so every team starts from the company baseline:

```toml
# registry.toml
version = 1
default_groups = ["base"]

[groups.base]
description = "For everyone"
instructions = ["company-standards", "security-basics"]
skills = ["code-review", "commit-style"]

[groups.platform]
description = "Platform team"
include = ["base"]
skills = ["terraform-review"]
instructions = ["terraform-style"]
agents = ["infra-reviewer"]
mcp = ["internal-docs"]

[groups.frontend]
description = "Frontend guild"
include = ["base"]
skills = ["a11y-check"]
```

Someone who subscribes without choosing a group gets `default_groups`.
People join the groups of the teams they work with:

```sh
spellbook group list acme
spellbook group join acme platform
spellbook group leave acme frontend
```

### Onboarding in three commands

Put this into your onboarding guide or setup script:

```sh
brew tap graemer-org/spellbook https://github.com/graemer-org/spellbook && brew install spellbook
spellbook init && spellbook registry add acme git@github.com:acme/ai-registry.git --group platform
spellbook autosync enable
```

Spellbook fetches with your own `git`, so whatever lets you clone the
registry (SSH keys, a credential helper, single sign-on) is all the access
control there is to set up. A repository it cannot reach is reported as an
error.

### Several registries, layered

A company registry and a team registry can be combined. `--priority`
decides the order instructions are read in and who wins a name collision;
`--prefix collisions` keeps both versions when names clash.

```sh
spellbook registry add company git@github.com:acme/ai-company.git --priority 100
spellbook registry add team git@github.com:acme/ai-platform.git --prefix collisions
```

### Change control

The registry is an ordinary repository, so the usual tools apply.

- **Review.** Protect the main branch and require pull requests. A change
  to an instruction or a skill changes what every agent in the company
  does, so review it like code.
- **Ownership.** A `CODEOWNERS` file gives each team its own folders.
- **Staged rollout.** Let most people follow a `stable` branch or a tag
  with `--ref stable`, and a few follow `main`.
- **Things that run code.** MCP servers and hooks are only installed where
  the person allowed them with `spellbook registry allow acme mcp hooks`.
  Publishing one in the registry does not put it on anyone's machine.
- **No secrets.** Refer to environment variables such as `${DOCS_TOKEN}`
  in MCP server definitions. Never commit a token to the registry.

### Projects

For repositories, use the project setup from the section above and add
`spellbook sync --project --check` to CI. That way a repository carries a
pinned, reviewed copy of the organization's tooling, and cloud agents see
it too.

## Writing a registry

Any git repository works as a registry.

- Every directory that contains a `SKILL.md` is a skill, named after its
  directory.
- Every Markdown file below `instructions/` is an instruction, below
  `agents/` an agent and below `prompts/` a prompt, each named after its
  file.
- Every JSON file in `mcp/` is an MCP server, named after its file.
  Servers listed in a `mcp.json` or `.mcp.json` at the root count too.
- Every JSON file in `hooks/`, or directory there with a `hook.json`, is a
  hook.

```
ai-registry/
├── registry.toml                  # optional: groups
├── instructions/
│   ├── company-standards.md
│   └── terraform-style.md
├── agents/
│   └── reviewer.md
├── prompts/
│   └── release-notes.md
├── mcp/
│   └── internal-docs.json
├── hooks/
│   ├── no-force-push.json
│   └── format/
│       ├── hook.json
│       └── format.sh
└── skills/
    ├── code-review/SKILL.md
    └── terraform-review/SKILL.md
```

- `--path platform` subscribes to one subdirectory only.
- `--skill code-review` installs only the named skills. `--instruction`,
  `--agent`, `--prompt`, `--mcp-server` and `--hook` do the same for their
  kind.
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

Files written for Copilot work as they are: `name.instructions.md` is
installed as `name`, `applyTo` is read like `paths`, and `applyTo: "**"`
means always.

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

### Agents

An agent is a Markdown file whose frontmatter needs a `description`.
Spellbook passes the frontmatter on as you wrote it, so every field a tool
understands works. Fields under `claude:` or `copilot:` go to that tool
only, which is how you give each one its own model:

```markdown
---
description: Reviews pull requests for correctness
tools: Read, Grep, Glob
claude:
  model: opus
copilot:
  model: gpt-5
---

You are a careful code reviewer. ...
```

Claude Code identifies an agent by its `name`, so there Spellbook sets it to
the name the agent is installed under. Copilot goes by the file name and
keeps `name` as a display name. Copilot accepts Claude Code's tool names, so
one `tools` list serves both. Files named `name.agent.md` are accepted too.

### Prompts

A prompt is a reusable task you start yourself. The same `claude:` and
`copilot:` sections work as for agents.

- Claude Code gets it as a skill that only you invoke, as
  `/release-notes`. Use `$ARGUMENTS` for what you type after the command.
- Copilot gets `.github/prompts/release-notes.prompt.md`. Copilot reads
  prompt files only from a project, so at home level Spellbook skips them
  with a warning. The two tools write arguments differently; Spellbook does
  not translate the body.

### MCP servers

An MCP server is one JSON file with the definition both tools expect under
`mcpServers`. As for agents, `claude` and `copilot` hold fields for one tool
only.

```json
{
  "command": "npx",
  "args": ["-y", "@acme/docs-mcp"],
  "env": { "DOCS_TOKEN": "${DOCS_TOKEN}" },
  "copilot": { "tools": ["search"] }
}
```

MCP servers start programs or connect to services on your machine. A
registry therefore installs them only after you allowed it:

```sh
spellbook registry allow acme mcp
```

Never put secrets into a registry. Refer to environment variables as in the
example; both tools expand `${VAR}`.

- In a project, Claude Code's servers go to `.mcp.json` and Copilot's to
  `.github/mcp.json`. Copilot requires a `tools` field, which Spellbook sets
  to all tools unless you give one.
- At home level, Copilot's servers go to `~/.copilot/mcp-config.json`.
  Claude Code keeps its user-level servers in `~/.claude.json`, a file it
  rewrites itself, so Spellbook changes them through `claude mcp` and needs
  the `claude` command on the `PATH`.
- Servers you added yourself stay untouched, also when a registry offers
  one with the same name.

### Hooks

A hook is a command a tool runs when something happens in a session. Like
MCP servers, hooks run on your machine, so a registry installs them only
after `spellbook registry allow acme hooks`.

```json
{
  "description": "Stop force pushes",
  "event": "PreToolUse",
  "matcher": "Bash",
  "command": "grep -q 'push --force' && exit 2 || exit 0",
  "timeout": 10
}
```

- `event` is one of `SessionStart`, `SessionEnd`, `UserPromptSubmit`,
  `PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `Stop`, `SubagentStop`,
  `PreCompact` and `Notification`. These are Claude Code's names. Copilot
  accepts them too and then uses the same matcher rules and the same input
  format, so one hook serves both tools.
- `matcher`, `timeout` (seconds) and `tools` are optional. `tools` limits a
  hook to `["claude"]` or `["copilot"]`.
- A hook that needs a script is a directory: `hooks/format/hook.json` next
  to its files. Spellbook installs the files and replaces `${HOOK_DIR}` in
  the command with where they are:

  ```json
  { "event": "PostToolUse", "matcher": "Edit|Write", "command": "\"${HOOK_DIR}/format.sh\"" }
  ```

Spellbook marks its entries with a comment behind the command
(`# spellbook:acme/format`). Your own hooks and every other setting in
`settings.json` stay as they are.

In a project with both tools enabled, a hook is written to
`.claude/settings.json` only: Copilot's command line reads that file too,
and a second entry would run the hook twice. Copilot's cloud agent reads
only `.github/hooks/`, so give a hook `"tools": ["copilot"]` if it has to
run there.

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
agents = ["infra-reviewer"]
prompts = ["release-notes"]
mcp = ["internal-docs"]
hooks = ["no-force-push"]
```

- A subscriber who chooses no group gets `default_groups`. Without
  `default_groups`, or without a `registry.toml`, they get everything.
- `include` pulls in everything from another group.
- `spellbook group join acme platform` adds a group to the ones you already
  have, `group leave` removes one, and `group list acme` shows what exists.

### Name collisions

When two registries offer a resource of the same kind and name, the registry
with the higher `--priority` wins and Spellbook prints a warning.
To keep both, give a registry a prefix:

- `--prefix collisions` installs under `<registry>-<name>` only when the
  plain name is already taken.
- `--prefix all` always installs under `<registry>-<name>`.

Spellbook rewrites the `name` in the skill's frontmatter to match.

## Reference

### Commands

```sh
spellbook init [--project] [--target claude] [--target copilot]

spellbook registry add <name> <git-url> [settings] [--no-sync]
spellbook registry set <name> [--url <git-url>] [settings]
spellbook registry list
spellbook registry remove <name> [--force]
spellbook registry setup <directory>
spellbook registry allow <name> mcp|hooks...
spellbook registry disallow <name> mcp|hooks...

spellbook group list <registry>
spellbook group join <registry> <group>...
spellbook group leave <registry> <group>...

spellbook sync [--dry-run] [--force] [--locked] [--check]
spellbook diff [registry] [--patch]
spellbook list

spellbook autosync enable|disable|status
spellbook doctor [--offline]
```

Every command takes `--project` (`-p`) to work on the project instead of the
home configuration.

The settings of a registry, for `registry add` and `registry set`:

| Flag | Meaning |
|---|---|
| `--ref main` | branch, tag or commit to follow |
| `--path teams/platform` | only this subdirectory of the repository |
| `--group platform` | group to join; repeatable |
| `--priority 10` | the higher number wins a name collision and comes first |
| `--prefix all\|collisions` | install under `<registry>-<name>` |
| `--only skills` | only this kind; repeatable |
| `--allow mcp` | let the registry install MCP servers or hooks; repeatable |
| `--skill`, `--instruction`, `--agent`, `--prompt`, `--mcp-server`, `--hook` | only the named resource of that kind; repeatable |

`registry set` changes only what you pass. An empty value clears a setting,
for example `--ref=` or `--skill=`.

### Where things go

| | Home | Project (`--project`) |
|---|---|---|
| Config | `~/.config/spellbook/config.toml` | `spellbook.toml` in the project root |
| Claude Code skills | `~/.claude/skills/` | `.claude/skills/` |
| GitHub Copilot skills | `~/.copilot/skills/` | `.github/skills/` |
| Claude Code instructions | `~/.claude/rules/` | `.claude/rules/` |
| GitHub Copilot instructions | `~/.copilot/copilot-instructions.md`, `~/.copilot/instructions/` | `.github/copilot-instructions.md`, `.github/instructions/` |
| Claude Code agents | `~/.claude/agents/` | `.claude/agents/` |
| GitHub Copilot agents | `~/.copilot/agents/` | `.github/agents/` |
| Claude Code prompts | `~/.claude/skills/` (as a skill) | `.claude/skills/` (as a skill) |
| GitHub Copilot prompts | not supported by Copilot | `.github/prompts/` |
| Claude Code MCP servers | `~/.claude.json`, through the `claude` command | `.mcp.json` |
| GitHub Copilot MCP servers | `~/.copilot/mcp-config.json` | `.github/mcp.json` |
| Claude Code hooks | `~/.claude/settings.json` | `.claude/settings.json` |
| GitHub Copilot hooks | `~/.copilot/hooks/spellbook-hooks.json` | `.github/hooks/spellbook-hooks.json` |
| Lock file | `~/.config/spellbook/spellbook.lock` | `spellbook.lock` |
| State | `~/.config/spellbook/state.json` | `.spellbook/state.json` |

### Reviewing and pinning updates

Every sync records the commit of each registry in `spellbook.lock`.

```sh
spellbook diff              # what changed upstream since the last sync
spellbook diff acme --patch # with the textual difference
spellbook sync              # move to the newest commits and update the lock
spellbook sync --locked     # install exactly what the lock records
spellbook sync --check      # fail if the installed files differ from the lock
```

### Automatic sync

`spellbook autosync enable` installs a session hook in each enabled tool
that runs `spellbook sync --auto` when a session starts:

- Claude Code: an entry in `~/.claude/settings.json`. Your other settings
  and hooks stay as they are.
- GitHub Copilot: the file `~/.copilot/hooks/spellbook.json`.

The automatic sync runs at most once a day, detached from the session that
started it, prints nothing and never fails a session. `spellbook autosync status` shows when it
last ran and whether it worked. Set the pace with `autosync.interval` in the
configuration.

### Configuration

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
# skills = ["terraform-review"]    # only these skills; likewise
#                                  # instructions, agents, prompts, mcp, hooks
# only = ["skills"]                # only these kinds: skills, instructions, agents, prompts, mcp, hooks
# allow = ["mcp", "hooks"]         # let this registry install MCP servers and hooks

[autosync]
interval = "12h"                   # default: 24h
```

### Troubleshooting

`spellbook doctor` checks the setup and says what to do about each finding:

- git, the configuration, and whether each enabled tool is installed
- whether each registry can be reached and has changes you have not synced
- whether every installed file still matches what was synced, or was edited
  or deleted
- how much the installed instructions add to every session, and which are
  the largest; the same for the number of skills
- environment variables that installed MCP servers refer to but that are
  not set
- in a project, synced files that are not committed yet
- an automatic sync that failed, never finished or has not run for long

`--offline` skips the checks that contact the registries.

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

## Contributing

Contributions are welcome. [CONTRIBUTING.md](CONTRIBUTING.md) explains how
to build, test and propose a change, and [SECURITY.md](SECURITY.md) how to
report a vulnerability.

## License

Spellbook is licensed under the [Apache License 2.0](LICENSE).
