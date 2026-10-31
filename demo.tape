# VHS tape for the README demo GIF: https://github.com/charmbracelet/vhs
# Render with `make demo-gif` (builds bin/headsup and puts it on PATH first).
# Runs against the built-in demo data, with the simulated host, so no
# headscale server is needed.

Output docs/demo.gif

Require headsup

Set Shell "bash"
Set FontSize 14
Set Width 1400
Set Height 820
Set Padding 12
Set TypingSpeed 80ms
Set Framerate 24

Hide
Type "export XDG_CONFIG_HOME=$(mktemp -d) XDG_STATE_HOME=$(mktemp -d) && clear"
Enter
Show

Type "headsup --demo --host"
Sleep 400ms
Enter
Sleep 3s

# Tailnet section: machines, filter, detail.
Type "1m"
Sleep 2s
Type "/"
Sleep 300ms
Type "alice"
Sleep 1.5s
Enter
Sleep 1.5s
Escape
Sleep 300ms
Escape
Sleep 1s

# Routes, then the policy editor.
Type "1r"
Sleep 2s
Type "1a"
Sleep 2.5s

# Server section: DNS, then the service page with live logs.
Type "2d"
Sleep 2.5s
Type "2l"
Sleep 3s

# Command palette.
Type ":"
Sleep 500ms
Type "expire"
Sleep 1.5s
Escape
Sleep 500ms

# Theme picker with live preview.
Type "T"
Sleep 1s
Down 3
Sleep 1.5s
Down 3
Sleep 1.5s
Escape
Sleep 1s

# Help.
Type "?"
Sleep 2.5s
Escape
Sleep 500ms

Type "q"
Sleep 500ms
