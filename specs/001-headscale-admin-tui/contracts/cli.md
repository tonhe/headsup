# Contract: Non-interactive CLI

```text
headsup                                  # launch TUI
headsup nodes   ls [--user U] [--filter Q] [-o table|wide|json|yaml]
headsup nodes   show ID|NAME
headsup nodes   rename ID NEW
headsup nodes   tags ID [--set a,b | --add a | --rm a]
headsup nodes   expire ID [--disable]
headsup nodes   delete ID [--yes]
headsup nodes   register --user U KEY|URL
headsup routes  ls [--pending]
headsup routes  approve ID PREFIX... | revoke ID PREFIX...
headsup users   ls | create NAME [--display-name --email] | rename ID NEW | delete ID [--yes]
headsup keys    preauth ls|create|expire|delete  (create: --user --reusable --ephemeral --expiration 24h --tags a,b --qr)
headsup keys    api ls|create|expire|delete      (create: --expiration 90d)
headsup policy  get [-o raw|json] | check FILE | diff FILE | set FILE [--yes] | history | rollback N
headsup auth    approve AUTH_ID [--user U] | reject AUTH_ID
headsup server  info | caps | backfill-ips [--yes]
headsup host    info | doctor | perms                        # environment, findings, fix commands
headsup svc     status | start | stop | restart | reload | logs [-f] [--level warn] [--grep RE]
headsup conf    get KEY | set KEY VALUE [--yes] [--no-restart] | diff | edit | migrate | rollback [N]
headsup dns     show | records ls|add NAME TYPE VALUE|rm NAME | set KEY VALUE
headsup derp    show | regions ls | map edit PATH
headsup backup  create | ls | restore ID [--files-only]
headsup upgrade check
headsup themes  ls | preview SLUG | reload
headsup config  path | theme SLUG | profile add|ls|rm|default
headsup version
```

## Rules

- Exit codes:

  | Code | Meaning |
  |---|---|
  | 0 | ok |
  | 1 | generic error |
  | 2 | usage error |
  | 3 | unauthorized |
  | 4 | not found |
  | 5 | unsupported by server |
  | 6 | validation failed |
  | 7 | aborted (not confirmed) |
  | 8 | host mode unavailable |
  | 9 | host permission denied |

- A destructive verb without `--yes` prompts on a TTY. When stdin is not a TTY it fails with
  exit code 7.
- `-o json` prints stable, documented field names (snake_case), whatever the server version.
- Data goes to stdout and messages go to stderr. Colour is off when stdout is not a TTY or
  `NO_COLOR` is set.
- Host verbs exit with code 8 when host mode is unavailable, and code 9 on a permission
  error. Either way they print the fix command to stderr.
- A mutating verb on a read-only profile (or with `--read-only`) exits with code 3.
- Mutating verbs print a one-line result to stderr; with `-o json|yaml|table` they also
  print the changed object to stdout. `keys * create` prints the new key on stdout.
- `conf diff FILE` compares the live config with FILE (the CLI has no staged edits).
  `conf edit`, `conf rollback` and `derp map edit` are interactive and exit 5 with a
  pointer to the TUI.
- `ID|NAME` arguments resolve a unique name to its ID. An ambiguous name lists the candidates
  and exits with code 2.
