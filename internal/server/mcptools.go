package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/h4ux/homeowl/internal/analytics"
	"github.com/h4ux/homeowl/internal/mcp"
	"github.com/h4ux/homeowl/internal/unifi"
)

func nextdnsTest() map[string]any {
	hc := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: nil}}
	resp, err := hc.Get("https://test.nextdns.io")
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	defer resp.Body.Close()
	var d any
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	_ = json.Unmarshal(body, &d)
	return map[string]any{"ok": true, "data": d}
}

// ------------------------------------------------------------------ bundle helpers

func (a *App) bundle(ctx context.Context) (map[string]any, error) {
	if !a.UniFi.Connected() {
		return nil, errors.New("HomeOwl isn't connected to a UniFi console (Settings › UniFi)")
	}
	return a.Cache.Get(false, a.maxAge()), nil
}

func section(b map[string]any, src, name string) []map[string]any {
	m, _ := b[src].(map[string]any)
	return unifi.List(m[name])
}

func pickKeys(m map[string]any, keys ...string) map[string]any {
	out := map[string]any{}
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil && v != "" {
			out[k] = v
		}
	}
	return out
}

func matches(m map[string]any, q string, keys ...string) bool {
	if q == "" {
		return true
	}
	q = strings.ToLower(q)
	for _, k := range keys {
		if strings.Contains(strings.ToLower(fmt.Sprint(m[k])), q) {
			return true
		}
	}
	return false
}

func argS(a map[string]any, k string) string { v, _ := a[k].(string); return strings.TrimSpace(v) }
func argB(a map[string]any, k string, def bool) bool {
	if v, ok := a[k].(bool); ok {
		return v
	}
	return def
}
func argN(a map[string]any, k string, def int) int {
	if v, ok := a[k].(float64); ok {
		return int(v)
	}
	return def
}

func obj(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func strP(desc string) map[string]any  { return map[string]any{"type": "string", "description": desc} }
func intP(desc string) map[string]any  { return map[string]any{"type": "integer", "description": desc} }
func boolP(desc string) map[string]any { return map[string]any{"type": "boolean", "description": desc} }
func enumP(desc string, v ...string) map[string]any {
	return map[string]any{"type": "string", "description": desc, "enum": v}
}

var dryRunP = boolP("true (default) = only preview what would change; false = apply it")

// ------------------------------------------------------------------ tools

func (a *App) allTools() []mcp.Tool {
	devKeys := []string{"name", "model", "type", "mac", "ip", "state", "version", "upgradable", "upgrade_to_firmware", "uptime", "adopted", "num_sta", "satisfaction"}
	cliKeys := []string{"name", "hostname", "mac", "ip", "network", "essid", "is_wired", "oui", "signal", "rssi", "tx_rate", "rx_rate", "uptime", "ap_mac", "sw_mac", "sw_port", "blocked", "use_fixedip", "fixed_ip", "last_seen"}
	findClient := func(b map[string]any, id string) map[string]any {
		id = strings.ToLower(id)
		for _, src := range []string{"clients", "known_clients"} {
			for _, c := range section(b, "legacy", src) {
				if strings.ToLower(fmt.Sprint(c["mac"])) == id || strings.EqualFold(fmt.Sprint(c["name"]), id) || strings.EqualFold(fmt.Sprint(c["hostname"]), id) || fmt.Sprint(c["ip"]) == id {
					return c
				}
			}
		}
		return nil
	}
	devAction := func(cmd string) func(ctx context.Context, args map[string]any, who string) (any, error) {
		return func(ctx context.Context, args map[string]any, who string) (any, error) {
			act := unifi.Action{"kind": "devcmd", "cmd": cmd, "mac": strings.ToLower(argS(args, "mac")), "description": "via MCP"}
			if cmd == "set-locate" && !argB(args, "on", true) {
				act["cmd"] = "unset-locate"
			}
			return a.Changes.Run(a.UniFi, act, argB(args, "dry_run", true), who, false)
		}
	}
	staAction := func(cmd string) func(ctx context.Context, args map[string]any, who string) (any, error) {
		return func(ctx context.Context, args map[string]any, who string) (any, error) {
			return a.Changes.Run(a.UniFi, unifi.Action{"kind": "stacmd", "cmd": cmd, "mac": strings.ToLower(argS(args, "mac")), "description": "via MCP"},
				argB(args, "dry_run", true), who, false)
		}
	}
	macArg := obj(map[string]any{"mac": strP("MAC address, e.g. aa:bb:cc:dd:ee:ff"), "dry_run": dryRunP}, "mac")

	return []mcp.Tool{
		{Name: "network_overview", Title: "Network overview", Level: "read",
			Description: "Summary of the home network: console, internet (WAN) status, device and client counts, networks, Wi-Fi, active alarms.",
			Schema:      obj(map[string]any{}),
			Run: func(ctx context.Context, args map[string]any, who string) (any, error) {
				b, err := a.bundle(ctx)
				if err != nil {
					return nil, err
				}
				devs := section(b, "legacy", "devices")
				off := 0
				for _, d := range devs {
					if fmt.Sprint(d["state"]) != "1" {
						off++
					}
				}
				health := []map[string]any{}
				for _, h := range section(b, "legacy", "health") {
					health = append(health, pickKeys(h, "subsystem", "status", "num_user", "num_ap", "num_sw", "wan_ip", "isp_name", "latency", "xput_down", "xput_up", "uptime"))
				}
				integ, _ := b["integration"].(map[string]any)
				return map[string]any{"console": integ["info"], "health": health, "devices": len(devs), "devices_offline": off,
					"clients_online": len(section(b, "legacy", "clients")), "known_clients": len(section(b, "legacy", "known_clients")),
					"networks": len(section(b, "legacy", "networks")), "wifi_networks": len(section(b, "legacy", "wlans")),
					"active_alarms": len(section(b, "legacy", "alarms")), "data_collected": b["meta"].(map[string]any)["collected_at"]}, nil
			}},
		{Name: "list_devices", Title: "List UniFi devices", Level: "read",
			Description: "UniFi gateways, switches, access points and other adopted devices with model, IP, firmware, state (1=online) and clients.",
			Schema:      obj(map[string]any{"query": strP("optional filter on name, model, IP or MAC")}),
			Run: func(ctx context.Context, args map[string]any, who string) (any, error) {
				b, err := a.bundle(ctx)
				if err != nil {
					return nil, err
				}
				var out []map[string]any
				for _, d := range section(b, "legacy", "devices") {
					if matches(d, argS(args, "query"), "name", "model", "ip", "mac") {
						out = append(out, pickKeys(d, devKeys...))
					}
				}
				return map[string]any{"devices": out}, nil
			}},
		{Name: "get_device", Title: "Device details", Level: "read",
			Description: "Full details of one UniFi device (ports, radios, uplink, stats) by MAC or name.",
			Schema:      obj(map[string]any{"device": strP("MAC address or name")}, "device"),
			Run: func(ctx context.Context, args map[string]any, who string) (any, error) {
				b, err := a.bundle(ctx)
				if err != nil {
					return nil, err
				}
				id := strings.ToLower(argS(args, "device"))
				for _, d := range section(b, "legacy", "devices") {
					if strings.ToLower(fmt.Sprint(d["mac"])) == id || strings.EqualFold(fmt.Sprint(d["name"]), id) {
						return d, nil
					}
				}
				return nil, errors.New("no device with that MAC or name")
			}},
		{Name: "list_clients", Title: "List clients", Level: "read",
			Description: "Phones, laptops, TVs and other clients: name, IP, network, Wi-Fi or wired, signal, which AP/switch port. Online clients by default.",
			Schema: obj(map[string]any{"query": strP("filter on name, hostname, IP, MAC, vendor or network"),
				"include_offline": boolP("also list known clients that are offline"), "limit": intP("max results (default 200)")}),
			Run: func(ctx context.Context, args map[string]any, who string) (any, error) {
				b, err := a.bundle(ctx)
				if err != nil {
					return nil, err
				}
				seen := map[string]bool{}
				var out []map[string]any
				srcs := []string{"clients"}
				if argB(args, "include_offline", false) {
					srcs = append(srcs, "known_clients")
				}
				for i, src := range srcs {
					for _, c := range section(b, "legacy", src) {
						mac := fmt.Sprint(c["mac"])
						if seen[mac] || !matches(c, argS(args, "query"), "name", "hostname", "ip", "mac", "oui", "network") {
							continue
						}
						seen[mac] = true
						r := pickKeys(c, cliKeys...)
						r["online"] = i == 0
						out = append(out, r)
					}
				}
				lim := argN(args, "limit", 200)
				if len(out) > lim {
					out = out[:lim]
				}
				return map[string]any{"clients": out, "count": len(out)}, nil
			}},
		{Name: "get_client", Title: "Client details", Level: "read",
			Description: "Everything known about one client by MAC, name, hostname or IP.",
			Schema:      obj(map[string]any{"client": strP("MAC, name, hostname or IP")}, "client"),
			Run: func(ctx context.Context, args map[string]any, who string) (any, error) {
				b, err := a.bundle(ctx)
				if err != nil {
					return nil, err
				}
				if c := findClient(b, argS(args, "client")); c != nil {
					return c, nil
				}
				return nil, errors.New("no client matches")
			}},
		{Name: "list_networks", Title: "Networks & Wi-Fi", Level: "read",
			Description: "Networks/VLANs (subnet, DHCP, isolation) and Wi-Fi networks (SSID, security, bands). Passwords are never included.",
			Schema:      obj(map[string]any{}),
			Run: func(ctx context.Context, args map[string]any, who string) (any, error) {
				b, err := a.bundle(ctx)
				if err != nil {
					return nil, err
				}
				var nets, wifi []map[string]any
				for _, n := range section(b, "legacy", "networks") {
					nets = append(nets, pickKeys(n, "_id", "name", "purpose", "vlan", "ip_subnet", "dhcpd_enabled", "dhcpd_start", "dhcpd_stop", "network_isolation_enabled", "internet_access_enabled", "mdns_enabled", "igmp_snooping"))
				}
				for _, w := range section(b, "legacy", "wlans") {
					wifi = append(wifi, pickKeys(w, "_id", "name", "enabled", "security", "wpa_mode", "wpa3_support", "pmf_mode", "networkconf_id", "is_guest", "hide_ssid", "wlan_bands", "l2_isolation", "fast_roaming_enabled"))
				}
				return map[string]any{"networks": nets, "wifi": wifi}, nil
			}},
		{Name: "list_firewall", Title: "Firewall & exposure", Level: "read",
			Description: "Firewall policies/zones, legacy firewall rules, port forwards and traffic rules.",
			Schema:      obj(map[string]any{}),
			Run: func(ctx context.Context, args map[string]any, who string) (any, error) {
				b, err := a.bundle(ctx)
				if err != nil {
					return nil, err
				}
				return map[string]any{"policies": section(b, "v2", "firewall_policies"), "zones": section(b, "v2", "firewall_zones"),
					"rules": section(b, "legacy", "firewall_rules"), "port_forwards": section(b, "legacy", "port_forwards"),
					"traffic_rules": section(b, "v2", "traffic_rules")}, nil
			}},
		{Name: "list_events", Title: "Recent events", Level: "read",
			Description: "Recent UniFi events and system-log entries (connections, disconnects, upgrades, threats), newest first.",
			Schema:      obj(map[string]any{"query": strP("optional text filter"), "limit": intP("max results (default 100)")}),
			Run: func(ctx context.Context, args map[string]any, who string) (any, error) {
				b, err := a.bundle(ctx)
				if err != nil {
					return nil, err
				}
				var out []map[string]any
				for _, e := range append(section(b, "legacy", "events"), section(b, "v2", "system_log")...) {
					if matches(e, argS(args, "query"), "msg", "message", "key", "title", "subsystem") {
						out = append(out, pickKeys(e, "time", "timestamp", "datetime", "key", "subsystem", "msg", "message", "title", "category", "severity"))
					}
				}
				sort.SliceStable(out, func(i, j int) bool { return evTime(out[i]) > evTime(out[j]) })
				if l := argN(args, "limit", 100); len(out) > l {
					out = out[:l]
				}
				return map[string]any{"events": out, "alarms": section(b, "legacy", "alarms")}, nil
			}},
		{Name: "usage_analytics", Title: "DNS & usage analytics", Level: "read",
			Description: "Who uses what and when, from NextDNS logs and UniFi traffic: totals, top domains/categories/devices/people, hour×weekday heatmap and data use. " +
				"Filters narrow everything (person id, device name, category like 'Social', domain root like 'tiktok.com', status 'blocked').",
			Schema: obj(map[string]any{
				"range":    enumP("period", "24h", "7d", "30d", "90d", "1y", "all"),
				"person":   strP("person id (see list_people)"),
				"device":   strP("device name as in NextDNS"),
				"category": strP("category, e.g. Social, Video & streaming, Gaming"),
				"root":     strP("root domain, e.g. youtube.com"),
				"status":   enumP("query status", "default", "blocked", "allowed"),
				"views": map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": []string{"summary", "timeline", "heatmap", "by_device", "by_domain", "by_category", "by_person", "by_status", "traffic"}},
					"description": "what to return (default: summary, by_category, by_domain, by_device)"},
			}),
			Run: func(ctx context.Context, args map[string]any, who string) (any, error) {
				views := []string{"summary", "by_category", "by_domain", "by_device"}
				if l, ok := args["views"].([]any); ok && len(l) > 0 {
					views = nil
					for _, v := range l {
						views = append(views, fmt.Sprint(v))
					}
				}
				f := map[string]any{}
				for _, k := range []string{"person", "device", "category", "root", "status"} {
					if v := argS(args, k); v != "" {
						f[k] = v
					}
				}
				return a.An.Explore(analytics.ExploreRequest{Range: orStr(argS(args, "range"), "7d"), Filters: f, Views: views,
					Page: map[string]any{"root_limit": 25, "device_limit": 25}})
			}},
		{Name: "list_people", Title: "People", Level: "read",
			Description: "The household members defined in HomeOwl and their devices (used by usage_analytics person filters).",
			Schema:      obj(map[string]any{}),
			Run: func(ctx context.Context, args map[string]any, who string) (any, error) {
				return map[string]any{"people": a.An.People()}, nil
			}},
		{Name: "analytics_sql", Title: "Analytics SQL (read-only)", Level: "read",
			Description: "Run one read-only DuckDB query on HomeOwl's analytics database. Tables: dns_queries, dns_hourly_device, dns_daily_domain, client_hourly, " +
				"site_hourly, sessions, clients, events, threats, syslog, syslog_hourly, people, person_devices; views dnsq (queries with device/category/person), trafq.",
			Schema: obj(map[string]any{"sql": strP("a single SELECT/WITH/DESCRIBE statement")}, "sql"),
			Run: func(ctx context.Context, args map[string]any, who string) (any, error) {
				return a.An.RunSQL(argS(args, "sql"), 1000)
			}},
		{Name: "change_log", Title: "Change log", Level: "read",
			Description: "Changes made through HomeOwl since it started (who, what, before/after, undoable).",
			Schema:      obj(map[string]any{}),
			Run: func(ctx context.Context, args map[string]any, who string) (any, error) {
				return map[string]any{"changes": a.Changes.List()}, nil
			}},
		{Name: "refresh_data", Title: "Refresh from UniFi", Level: "read",
			Description: "Re-read the UniFi console now (data is otherwise cached for a few minutes).",
			Schema:      obj(map[string]any{}),
			Run: func(ctx context.Context, args map[string]any, who string) (any, error) {
				if !a.UniFi.Connected() {
					return nil, errors.New("not connected to UniFi")
				}
				b := a.Cache.Get(true, 0)
				return map[string]any{"ok": true, "collected_at": b["meta"].(map[string]any)["collected_at"]}, nil
			}},

		// ---- guarded changes (allow-listed, previewed, logged, undoable)
		{Name: "block_client", Title: "Block a client", Level: "guarded", Destructive: true,
			Description: "Block a client from the network (reversible with unblock_client). Call with dry_run=true first.", Schema: macArg, Run: staAction("block-sta")},
		{Name: "unblock_client", Title: "Unblock a client", Level: "guarded",
			Description: "Allow a blocked client back on the network.", Schema: macArg, Run: staAction("unblock-sta")},
		{Name: "reconnect_client", Title: "Reconnect a Wi-Fi client", Level: "guarded",
			Description: "Kick a Wi-Fi client so it reconnects (useful after changing its settings).", Schema: macArg, Run: staAction("kick-sta")},
		{Name: "restart_device", Title: "Restart a UniFi device", Level: "guarded", Destructive: true,
			Description: "Restart an AP, switch or gateway (clients lose connection briefly; restarting the gateway drops internet for a few minutes).",
			Schema:      macArg, Run: devAction("restart")},
		{Name: "locate_device", Title: "Blink a device's LED", Level: "guarded",
			Description: "Turn the locate (flashing LED) on or off on a UniFi device.",
			Schema:      obj(map[string]any{"mac": strP("device MAC"), "on": boolP("true = start blinking, false = stop"), "dry_run": dryRunP}, "mac"),
			Run:         devAction("set-locate")},
		{Name: "apply_change", Title: "Change a setting (allow-listed)", Level: "guarded", Destructive: true,
			Description: "Apply one allow-listed change, the same ones the HomeOwl UI offers. action examples: " +
				`{"kind":"rest","resource":"wlanconf","id":"<_id>","fields":{"enabled":false}}, ` +
				`{"kind":"rest","resource":"user","id":"<client _id>","fields":{"use_fixedip":true,"fixed_ip":"192.168.1.20","network_id":"<_id>"}}, ` +
				`{"kind":"setting","key":"usg","fields":{"upnp_enabled":false}}, {"kind":"radio","device_id":"<_id>","radio":"na","fields":{"channel":"36"}}, ` +
				`{"kind":"route","network":"10.8.0.0/24","nexthop":"192.168.1.5"}. Resources: wlanconf, portforward, networkconf, user. Always dry_run first.`,
			Schema: obj(map[string]any{"action": map[string]any{"type": "object", "description": "the change"}, "dry_run": dryRunP}, "action"),
			Run: func(ctx context.Context, args map[string]any, who string) (any, error) {
				act, _ := args["action"].(map[string]any)
				if act == nil {
					return nil, errors.New("action is required")
				}
				res, err := a.Changes.Run(a.UniFi, unifi.Action(act), argB(args, "dry_run", true), who, false)
				if err == nil && !argB(args, "dry_run", true) {
					a.Cache.Invalidate()
				}
				return res, err
			}},
		{Name: "undo_change", Title: "Undo a change", Level: "guarded",
			Description: "Undo a change by its id from change_log.",
			Schema:      obj(map[string]any{"id": intP("change id")}, "id"),
			Run: func(ctx context.Context, args map[string]any, who string) (any, error) {
				err := a.Changes.Undo(a.UniFi, argN(args, "id", 0), who, false)
				a.Cache.Invalidate()
				return map[string]any{"ok": err == nil}, err
			}},

		// ---- full access ("god mode")
		{Name: "unifi_request", Title: "Raw UniFi API request", Level: "god", Destructive: true,
			Description: "Call any UniFi OS / Network API endpoint directly (full access mode only). Paths start at the console root, e.g. " +
				"/proxy/network/integration/v1/sites, /proxy/network/api/s/default/stat/device, /proxy/network/v2/api/site/default/trafficrules. " +
				"Non-GET requests change the console immediately and are logged; there is no preview or undo.",
			Schema: obj(map[string]any{"method": enumP("HTTP method", "GET", "POST", "PUT", "PATCH", "DELETE"), "path": strP("path starting with /"),
				"body": map[string]any{"description": "JSON body for POST/PUT/PATCH"}}, "method", "path"),
			Run: func(ctx context.Context, args map[string]any, who string) (any, error) {
				p := argS(args, "path")
				if !strings.HasPrefix(p, "/") || strings.Contains(p, "..") {
					return nil, errors.New("path must start with /")
				}
				if !a.UniFi.Connected() {
					return nil, errors.New("not connected to UniFi")
				}
				st, d := a.Changes.Raw(a.UniFi, strings.ToUpper(argS(args, "method")), p, args["body"], who)
				if strings.ToUpper(argS(args, "method")) != "GET" {
					a.Cache.Invalidate()
				}
				return map[string]any{"status": st, "data": unifi.Redact(d)}, nil
			}},
	}
}

func evTime(m map[string]any) float64 {
	for _, k := range []string{"time", "timestamp"} {
		if v, ok := m[k].(float64); ok {
			return v
		}
	}
	return 0
}
