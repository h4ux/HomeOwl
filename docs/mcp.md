# HomeOwl MCP server

HomeOwl speaks the [Model Context Protocol](https://modelcontextprotocol.io) so an AI assistant can use your network data.

- **HTTP**: `POST https://<homeowl>/mcp` with `Authorization: Bearer <token>` (Streamable HTTP, JSON responses).
- **stdio**: `homeowl mcp --url https://127.0.0.1:8555/mcp --token <token>` for clients that start a command (Claude Desktop). Add `--insecure` when pointing at another computer's self-signed certificate.

Create tokens and choose the mode in **Settings › AI & MCP**. Tokens can be revoked at any time; each change is logged with the token's name.

## Modes and tools

| Tool | Mode | What it does |
|---|---|---|
| `network_overview` | read | console, internet status, device/client counts, alarms |
| `list_devices`, `get_device` | read | UniFi gateways, switches, APs and their details |
| `list_clients`, `get_client` | read | clients with IP, network, Wi-Fi/wired, signal, uplink |
| `list_networks` | read | networks/VLANs and Wi-Fi (no passwords) |
| `list_firewall` | read | firewall policies/zones/rules, port forwards, traffic rules |
| `list_events` | read | recent events, system log and alarms |
| `usage_analytics` | read | DNS & data usage by person, device, category, domain, hour and weekday |
| `list_people` | read | household members and their devices |
| `analytics_sql` | read | one read-only DuckDB query |
| `change_log`, `refresh_data` | read | changes made through HomeOwl; re-read UniFi |
| `block_client`, `unblock_client`, `reconnect_client` | guarded | client actions (dry run by default) |
| `restart_device`, `locate_device` | guarded | device actions (dry run by default) |
| `apply_change`, `undo_change` | guarded | allow-listed setting changes (same as the UI) and undo |
| `unifi_request` | full access | any UniFi API request — no preview, no undo |

Guarded tools take `dry_run` (default `true`): the assistant shows you the preview and only applies it when called again with `dry_run: false`.

## Example questions

- "Which devices are offline and why?"
- "How much time did the kids spend on games last week, and at what hours?"
- "Is anything exposed to the internet?"
- "Block the unknown device that joined last night." (guarded mode)
