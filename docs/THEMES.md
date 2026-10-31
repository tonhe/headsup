# Themes

headsup ships 61 built-in themes and loads your own from theme files. A theme
is a Base16 palette: 16 colours, `base00` through `base0F`. Apart from policy
syntax highlighting, the UI doesn't read those slots directly. It reads
semantic roles (accent, online, danger, tag chip and so on) derived from the
palette, and a theme file can override any role without touching the rest.

## Where theme files go

Put files in the `themes` directory next to the config file:

| Platform | Directory |
|---|---|
| Linux, macOS | `$XDG_CONFIG_HOME/headsup/themes/`, or `~/.config/headsup/themes/` |
| Windows | `%APPDATA%\headsup\themes\` (unless `XDG_CONFIG_HOME` is set) |

`headsup config path` prints the config file's location. Files ending in
`.toml`, `.yaml` or `.yml` load at startup. To load changes without
restarting, press `ctrl+r` in the theme picker (`T`) or run
`headsup themes reload`, which also reports any problems.

## Writing a theme in TOML

TOML is the native format. Start from
[`themes/midnight-harbor.toml`](../themes/midnight-harbor.toml) in this repo:

```toml
name  = "Midnight Harbor"
slug  = "midnight-harbor"   # optional
light = false               # optional

base00 = "#0b1020"  # background
base01 = "#131a2e"  # panels, sidebar
base02 = "#1e2742"  # selection
base03 = "#4a5578"  # muted text, borders
base04 = "#8792b3"  # idle status, muted text on light themes
base05 = "#c8d0e8"  # foreground
base06 = "#e1e6f5"
base07 = "#ffffff"  # strong foreground
base08 = "#ff6b81"  # red: danger, offline, expired
base09 = "#ffa65c"  # orange: expiring
base0A = "#ffd866"  # yellow: pending, warnings
base0B = "#7bd88f"  # green: online, OK
base0C = "#5ad4e6"  # cyan: IP addresses, links
base0D = "#7aa2f7"  # blue: accent, focused border
base0E = "#c792ea"  # magenta: tags
base0F = "#b07a5b"  # brown: policy syntax (preprocessor tokens)

[roles]             # optional overrides
accent = "base0E"
tag_bg = "#2a1f3d"
```

The keys:

- `name` is what the theme picker shows. `scheme` works too. Without either,
  the file name is used.
- `slug` is the ID for `--theme`, `ui.theme` in the config and
  `headsup themes preview`. Without it, the slug comes from the name:
  "Midnight Harbor" becomes `midnight-harbor`.
- `light` marks a light theme for the picker's dark/light filter and for the
  muted-text rule below. `variant = "light"` or `"dark"` does the same. Without
  either, a theme is light when `base00` is a light colour.
- `base00` to `base0F` are all required. Key case doesn't matter. Values are
  `#rrggbb`, quoted. The base keys can also sit in a `[palette]` table.
- `[roles]` overrides individual roles (next section).

## Base16 YAML

A standard Base16 scheme file from the tinted-theming project loads unchanged:
`scheme`, `author` and `base00` to `base0F`, with or without the leading `#`.
The newer layout with a `palette:` mapping and `variant: light` works too. You
can add a `roles:` mapping, the same as the TOML `[roles]` table.

## Roles

Every colour in the UI comes from one of these roles. The defaults come from
the palette as shown. Override any of them under `[roles]` with a base key
(`"base0E"`) or a hex colour (`"#2a1f3d"`). Role names are snake_case, and
`tagBg` or `tag-bg` are read as `tag_bg`.

| Role | Default | Used for |
|---|---|---|
| `bg` | base00 | background |
| `panel` | base01 | panels and the sidebar |
| `selection` | base02 | the selected row |
| `fg` | base05 | normal text |
| `fg_strong` | base07 | emphasised text |
| `muted` | base03 (base04 on light themes) | help text, hints, secondary columns |
| `border` | base03 | pane borders |
| `border_focus` | base0D | the focused pane's border |
| `accent` | base0D | header, highlights, palette matches |
| `online` | base0B | online status dot |
| `offline` | base08 | offline status dot |
| `idle` | base04 | idle status dot |
| `expiring` | base09 | keys and machines close to expiry |
| `expired` | base08 | expired keys and machines |
| `pending` | base0A | routes and devices awaiting approval |
| `warn` | base0A | warnings |
| `danger` | base08 | delete, critical confirmations, errors |
| `ok` | base0B | success, approved |
| `tag_fg` | base0E | tag chip text |
| `tag_bg` | base00 blended 18% toward base0E | tag chip background |
| `ip` | base0C | addresses |
| `link` | base0C | links |

The dashboard sparklines use a fixed green to gold to red ramp, tuned
separately for dark and light themes, because Base16 accents tend to blend
into muddy colours. The ramp can't be overridden.

Policy syntax highlighting in Access controls reads the palette directly,
following the Base16 styling guidelines: comments base03, keys base0D, strings
base0B, numbers and constants base09, keywords base0E, preprocessor tokens
base0F, errors base08.

## Validation

- A missing base key, or a value that isn't `#rrggbb`, skips the file with a
  warning that names the file and the key.
- An unknown role, or a role value that is neither a base key nor a hex colour,
  is ignored with a warning. The rest of the theme loads.
- If the contrast between `fg` (base05) and `bg` (base00) is below 4.5:1, the
  theme still loads, with a warning.
- When two files use the same slug, the one loaded last wins (files load in
  name order), with a warning. A user theme with a built-in's slug replaces
  the built-in.

Warnings print to stderr at startup, show as toasts when you reload from the
theme picker, and appear in the output of `headsup themes reload`, which
exits non-zero when any file has problems.

## Trying a theme

- `T` opens the theme picker: live preview as you move, `/` to search, `tab`
  for the dark/light filter, `enter` to keep it, `esc` to go back to the
  previous theme. `ctrl+t` saves the choice for the current profile only.
- `--theme SLUG` sets it for one session.
- `ui.theme` in the config sets the default, and `theme` inside a
  `[[profile]]` sets it per connection, handy for marking production in red.
- `headsup themes ls` lists every theme and `headsup themes preview SLUG`
  prints its palette.

## Colour depth

Themes are truecolor. On a 256-colour or 16-colour terminal, colours are
downsampled. With `NO_COLOR` set to anything, headsup draws without colour.
If you need more contrast, copy `themes/high-contrast.toml` from this repo
into your themes directory. Its text and status colours all clear 7:1 against
the background.

## Adding a built-in theme

Built-in themes live in `internal/theme/themes.go`, the only place in the
codebase allowed to hold colour literals (a lint test enforces it). Add an
entry with `Name`, `Slug`, `Light` and the 16 base colours, and keep it in its
family group. Check the fg/bg contrast, and look at the Machines, Access
controls and Keys pages before sending a PR, since those use the most roles.
