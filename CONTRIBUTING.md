# Contributing to headsup

Bug reports, themes, fixes and new pages are all welcome. This file covers how
the project is organised and what a PR needs to get merged.

## Before you start

You need Go (the version in `go.mod`) and `make`. No headscale server is
required: `make demo` runs the TUI against built-in demo data, and
`./bin/headsup --demo --host` adds a simulated host for the Server pages.

```bash
git clone https://github.com/tonhe/headsup.git
cd headsup
make build        # bin/headsup
make test-all     # what CI runs, and it must pass before you push
make install      # build and copy to ~/bin
```

For anything bigger than a bug fix, open an issue first so we can agree on
the shape before you write the code.

## Spec-driven workflow

Features start as specs, before any code. Specs live in `specs/`, one
directory per feature (`specs/001-headscale-admin-tui/` is the current one):

- `spec.md`: user stories, requirements and success criteria.
- `plan.md`, `research.md`, `data-model.md`: the design and the decisions
  behind it.
- `contracts/`: the interfaces other code relies on (modules, backend, host,
  CLI, config, theme files). Change the contract in the same PR as the code.
- `tasks.md`: the work, as numbered tasks. Mark a task `[X]` in the PR that
  finishes it.

All of it is governed by the constitution in
`.specify/memory/constitution.md`. It's short and worth reading. When a PR
and the constitution disagree, the constitution wins, or the PR changes the
constitution on purpose and says why.

The specs were written with [Spec Kit](https://github.com/github/spec-kit)
(the templates are in `.specify/templates/`), but plain Markdown edits are
fine.

## Tests

`make test-all` runs, in order:

1. `make build`.
2. `go test -race ./...`.
3. `scripts/check-coverage.sh`, which checks each package in
   `scripts/coverage-floors.txt` against its floor.

Coverage floors only go up. If your change raises a package's coverage, you
can raise its floor. Never lower one to make a PR pass. Every bug fix ships
with a test that fails without the fix.

Views have golden snapshots in each package's `testdata/`. If you changed a
layout on purpose, run `make golden-update`, read the diff, and commit it.
[docs/MODULES.md](docs/MODULES.md) covers goldens and the `uitest.Driver`
for flow tests.

`docs/CAPABILITIES.md` is partly generated from the code. If you add or
change an action, a backend method or a capability, run `make docs` and
commit the result. A test fails while it's stale.

CI (`.github/workflows/test.yml`) builds and runs the race tests on Linux,
macOS and Windows, and the coverage gate on Linux. Path handling, file modes
and line endings differ between them, so a test that passes only on your
machine isn't done.

## Rules enforced by tests

These are lint tests in the normal test suite, so `make test-all` catches
them:

- **No colour literals outside `internal/theme`.** No `lipgloss.Color(` call
  and no quoted `#rrggbb` anywhere else in non-test code. Read colours from
  `ctx.Styles()` (`internal/theme/nocolor_test.go`).
- **No privilege escalation.** Nothing may run `sudo`, `su`, `doas` or
  `pkexec`, except `cmd/headsup/elevate.go`: the startup prompt that offers to
  restart under sudo and only does it after the user says yes. Showing a fix
  command such as `sudo usermod -aG headscale $USER` as text is fine
  (`internal/host/lint_test.go`).
- **No writes to the headscale database.** `internal/host/db` opens it
  read-only, and a test rejects write SQL there. Backups copy the database and
  print restore steps. They never write it.

Two more rules that review catches, since no test does yet:

- The product name is a placeholder. It lives in `internal/brand` and the Go
  module path only. In code, use `brand.Name` and `brand.Display`.
- Modules never import other modules. Cross-module jumps use
  `ctx.Navigate`.

## Code style

- `gofmt`. CLI flags use the stdlib `flag` package.
- Modules talk to headscale only through `api.Backend` and to the machine only
  through `host.Host`. No direct file or process access from a module.
- Host file writes go through the managed-file pipeline in
  `internal/host/files`: back up, validate, diff, confirm, atomic write,
  rollback. Don't add a second way to write a file.
- Errors say what went wrong and what to try next.
- Secrets never reach a log, a toast, the activity log or a backup manifest.

## Themes

Theme files are an easy first contribution. [docs/THEMES.md](docs/THEMES.md)
covers the format and the role table. A new built-in theme goes in
`internal/theme/themes.go`.

## New pages

[docs/MODULES.md](docs/MODULES.md) walks through adding a module: one
directory, one import line, actions as the single source for keys, palette,
help and CLI.

## Commits and PRs

- Branch from `main`. One topic per PR.
- Say what changed and why. Reference the task numbers from `tasks.md`.
- Add a line under `[Unreleased]` in `CHANGELOG.md` for anything a user
  would notice.
- Run `make test-all` before you push.

By contributing you agree that your work is released under the MIT license
in `LICENSE`.
