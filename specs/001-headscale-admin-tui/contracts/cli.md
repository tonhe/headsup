# Contract: Non-interactive CLI

```text
fathom                                  # launch TUI
fathom nodes   ls [--user U] [--filter Q] [-o table|wide|json|yaml]
fathom nodes   show ID|NAME
fathom nodes   rename ID NEW
fathom nodes   tags ID [--set a,b | --add a | --rm a]
fathom nodes   expire ID [--disable]
fathom nodes   delete ID [--yes]
fathom nodes   register --user U KEY|URL
fathom routes  ls [--pending]
fathom routes  approve ID PREFIX... | revoke ID PREFIX...
fathom users   ls | create NAME [--display-name --email] | rename ID NEW | delete ID [--yes]
fathom keys    preauth ls|create|expire|delete  (create: --user --reusable --ephemeral --expiration 24h --tags a,b --qr)
fathom keys    api ls|create|expire|delete      (create: --expiration 90d)
fathom policy  get [-o raw|json] | check FILE | diff FILE | set FILE [--yes] | history | rollback N
fathom auth    approve AUTH_ID [--user U] | reject AUTH_ID
fathom server  info | caps | backfill-ips [--yes]
fathom host    info | doctor | perms                        # environment, findings, fix commands
fathom svc     status | start | stop | restart | reload | logs [-f] [--level warn] [--grep RE]
fathom conf    get KEY | set KEY VALUE [--yes] [--no-restart] | diff | edit | migrate | rollback [N]
fathom dns     show | records ls|add NAME TYPE VALUE|rm NAME | set KEY VALUE
fathom derp    show | regions ls | map edit PATH
fathom backup  create | ls | restore ID [--files-only]
fathom upgrade check
fathom themes  ls | preview SLUG | reload
fathom config  path | theme SLUG | profile add|ls|rm|default
fathom version
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

- A destructive verb without `--yes` prompts on a TTY. When stdin is not a TTY it fails with
  exit code 7.
- `-o json` prints stable, documented field names (snake_case), whatever the server version.
- Data goes to stdout and messages go to stderr. Colour is off when stdout is not a TTY or
  `NO_COLOR` is set.
- Host verbs exit with code 8 when host mode is unavailable, and code 9 on a permission
  error. Either way they print the fix command to stderr.
- `ID|NAME` arguments resolve a unique name to its ID. An ambiguous name lists the candidates
  and exits with code 2.
