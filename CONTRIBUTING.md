# Contributing to Spellbook

Thank you for wanting to improve Spellbook. Bug reports, ideas and pull
requests are all welcome.

## Reporting a problem

Open an issue and include:

- what you ran and what you expected,
- the output, and the output of `spellbook doctor`,
- your operating system and `spellbook --version`.

For anything that could be a security problem, follow
[SECURITY.md](SECURITY.md) instead of opening an issue.

## Building and testing

You need Go (the version in `go.mod`) and `git`.

```sh
go build ./cmd/spellbook
go test ./...
go vet ./... && gofmt -l .     # both must be clean
```

The tests in `internal/cli` run the real command against local git
repositories in a temporary home directory, so they never touch your own
setup. One test stands in for the `claude` command with a small Python
script and is skipped when `python3` is missing.

## Making a change

1. Open an issue first for anything larger than a small fix, so the
   direction is clear before you write code.
2. Add a test in `internal/cli` for every behaviour a user can see.
3. Keep the rules in [CLAUDE.md](CLAUDE.md) under "Rules that must hold".
   The short version: Spellbook only changes what it installed, never
   overwrites local edits without `--force`, and anything that runs code on
   a user's machine needs explicit permission.
4. Update the README when a command, a flag or a file format changes.

## Commit messages

Commits follow [Conventional Commits](https://www.conventionalcommits.org),
because the next version and the changelog are derived from them:

| Prefix | Meaning | Release |
|---|---|---|
| `feat:` | something new for users | minor |
| `fix:` | a repair | patch |
| `feat!:` or a `BREAKING CHANGE:` footer | an incompatible change | breaking |
| `docs:`, `test:`, `refactor:`, `chore:`, `ci:` | everything else | none |

Pull requests are squashed, so the pull request title is what counts.

## Releases

[release-please](https://github.com/googleapis/release-please) keeps a
release pull request open with the next version and the changelog. Merging
it creates the tag and the GitHub release. The workflow then builds archives
for macOS and Linux on Intel and ARM, adds them to the release and commits
the updated `Formula/spellbook.rb`, which is what `brew install spellbook`
reads.

`CHANGELOG.md`, `.release-please-manifest.json` and `Formula/spellbook.rb`
are written by that workflow. Please do not edit them by hand.

## License

By contributing you agree that your contribution is licensed under the
[Apache License 2.0](LICENSE).
