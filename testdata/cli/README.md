# CLI fixtures

Recorded-style `headscale ... -o json` invocations for `internal/api/execcli`
tests, one JSON file per run: `{name, args, exit, stdout, stderr}`.
`<tmp>` in `args` stands for a temporary policy file.

These are **hand-built from the CLI sources** (headscale v0.29.6 and main
`cmd/headscale/cli/*.go`), pending real recordings. Replace them with captured
output when available.

- `v029/`: `encoding/json` over protobuf Go structs: snake_case keys, numeric
  IDs, timestamps as `{seconds, nanos}`, `register_method` as an integer, zero
  values omitted. Errors on stderr as `{"error": "... rpc error: code = X desc = ..."}`.
- `v030/`: oapi-codegen client types: camelCase, string IDs, RFC 3339. The
  exact wording of v0.30 error strings is an approximation.
