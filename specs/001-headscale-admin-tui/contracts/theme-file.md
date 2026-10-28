# Contract: User Theme Files

Location: `$XDG_CONFIG_HOME/headsup/themes/`. Files ending in `*.toml`, `*.yaml`, or `*.yml`
are loaded at startup. `headsup themes reload` and the theme picker's reload key load them again.

## TOML (native)

```toml
name  = "Midnight Harbor"
slug  = "midnight-harbor"      # optional; derived from name
light = false

base00 = "#0b1020"  # background
base01 = "#131a2e"  # panels / sidebar
base02 = "#1e2742"  # selection
base03 = "#4a5578"  # muted / borders
base04 = "#8792b3"
base05 = "#c8d0e8"  # foreground
base06 = "#e1e6f5"
base07 = "#ffffff"
base08 = "#ff6b81"  # red    → danger, offline
base09 = "#ffa65c"  # orange → expiring
base0A = "#ffd866"  # yellow → pending, warnings
base0B = "#7bd88f"  # green  → online, approved
base0C = "#5ad4e6"  # cyan   → IPs, links
base0D = "#7aa2f7"  # blue   → accent, focus
base0E = "#c792ea"  # magenta→ tags
base0F = "#b07a5b"  # brown  → misc

[roles]             # optional overrides; values are base keys or hex
accent  = "base0E"
tag_bg  = "#2a1f3d"
```

## Base16 YAML (tinted-theming compatible)

A standard `scheme`/`author`/`base00..base0F` YAML file is accepted unchanged.

## Role derivation (defaults)

| Role | Default | Used for |
|---|---|---|
| bg / panel / selection | 00 / 01 / 02 | backgrounds |
| fg / muted / border | 05 / 03 / 03 | text, help, borders |
| border_focus, accent | 0D | focused pane, header, palette match |
| online / offline / idle | 0B / 08 / 04 | status dots |
| expiring / expired | 09 / 08 | badges |
| pending / warn | 0A | routes awaiting approval |
| danger | 08 | confirm modal for critical actions, delete |
| tag_fg / tag_bg | 0E / blend(00, 0E, 15%) | tag chips |
| ip | 0C | addresses |
| spark ramp | `SparkGradient()` | dashboard sparklines |

## Validation

- All 16 base keys are required. Each must be `#rrggbb`.
- An invalid file is skipped, with a warning toast that names the file and the key.
- The contrast between `fg` and `bg` must be at least 4.5:1. If it is lower, the theme still
  loads with a warning.
