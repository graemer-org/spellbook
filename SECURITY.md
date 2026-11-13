# Security

## Reporting a vulnerability

Please report security problems privately, not in a public issue.

Use GitHub's private reporting: open the
[Security tab](https://github.com/graemer-org/spellbook/security) of this
repository and choose "Report a vulnerability". Describe what you found, how
to reproduce it and what an attacker could do with it.

You will get an answer as soon as someone has looked at it. Please give us
time to release a fix before you publish details.

Fixes are released for the latest version only.

## What Spellbook does on your machine

Spellbook copies content from git repositories you subscribe to into the
configuration of your AI tools. That content steers what those tools do, so
a registry deserves the same trust as code you run.

- **Subscribe only to registries you trust**, and review their changes.
  `spellbook diff --patch` shows what a sync would bring.
- **Skills, instructions, agents and prompts are text for the model.** A
  skill may also contain scripts that the agent can decide to run.
- **MCP servers and hooks run programs directly.** Spellbook installs them
  only for a registry you allowed with `spellbook registry allow <name>
  mcp` or `hooks`. Publishing one in a registry does not install it
  anywhere.
- **A registry can import from other repositories.** Subscribing to it
  means trusting what it imports as well. Imports bring skills,
  instructions, agents and prompts only, never MCP servers or hooks, and
  only one level deep.
- **Pin what must not change unnoticed.** `--ref` follows a tag or a commit
  instead of a branch, and `spellbook.lock` records the exact commit of
  every sync.
- **Spellbook only changes what it installed.** Files and settings you
  created yourself are left alone, and so are files you edited after a
  sync, unless you pass `--force`.

## Secrets

Never put tokens or passwords into a registry. MCP server definitions can
refer to environment variables such as `${DOCS_TOKEN}`, which both tools
expand when they start the server.
