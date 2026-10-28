# Debug endpoint fixtures

**These fixtures are hand-built**, pending `scripts/record-debug-fixtures.sh`
recording them from real headscale v0.29.6 (`v029/`) and main (`v030/`) containers.

They were written from the handler source at the two refs:

| File | Source | Shape |
|---|---|---|
| `registration-cache.json` | `hscontrol/debug.go` -> `state.DebugRegistrationCache()` | JSON object of counters only (`type`, `expiration`, `max_entries`, `current_len`, `status`); no entries in either version |
| `routes.json` | `state.DebugRoutes()` -> `types.DebugRoutes` (`Accept: application/json`) | `{"available_routes": {"<nodeID>": ["<prefix>"]}, "primary_routes": {"<prefix>": <nodeID>}, "unhealthy_nodes": [..] (omitempty)}` |
| `routes.txt` | `state.DebugRoutesString()` (default, `text/plain`) | `<prefix>: <nodeID>` per line, sorted |
| `ping-*.html` | `templates.PingPage` (GET `/debug/ping?node=<id>`) | HTML only; result text `Node N responded in 23ms`, `Node N did not respond. ...`, `Node N is not connected.` (0.29.6 adds a logo and footer) |
| `metrics.txt` | promhttp `/metrics` | Prometheus text format |

Values (`max_entries`, latencies, counts) are illustrative.
