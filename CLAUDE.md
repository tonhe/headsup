# Working rules for this repo

- Spec-driven: specs live in `specs/`, governed by `.specify/memory/constitution.md`.
  Mark tasks `[X]` in `specs/<feature>/tasks.md` as they land.
- The product name is a placeholder. It lives only in `internal/brand` and the Go
  module path; never hard-code it elsewhere.
- No colour literals outside `internal/theme` (enforced by a lint test).
- Modules never import other modules; cross-module jumps use `Context.Navigate`.
- Host code never runs sudo/su/doas/pkexec and never writes the headscale DB
  (enforced by lint tests).
- `make test-all` must pass before pushing. `make install` is the standard build.
- In shell commands use `\cat`, not `cat` (cat is aliased to bat on this machine).
