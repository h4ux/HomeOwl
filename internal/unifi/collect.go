package unifi

import (
	"fmt"
	"sync"
	"time"
)

var integrationSite = [][2]string{
	{"devices", "/devices"}, {"clients", "/clients"}, {"networks", "/networks"}, {"wifi", "/wifi/broadcasts"},
	{"firewall_zones", "/firewall/zones"}, {"firewall_policies", "/firewall/policies"}, {"acl_rules", "/acl-rules"},
	{"wans", "/wans"}, {"vpn_servers", "/vpn/servers"}, {"vpn_tunnels", "/vpn/site-to-site-tunnels"},
	{"dns_policies", "/dns/policies"}, {"radius_profiles", "/radius/profiles"},
}

var legacyEndpoints = [][2]string{
	{"sysinfo", "stat/sysinfo"}, {"health", "stat/health"}, {"devices", "stat/device"}, {"devices_basic", "stat/device-basic"},
	{"clients", "stat/sta"}, {"known_clients", "rest/user"}, {"networks", "rest/networkconf"}, {"wlans", "rest/wlanconf"},
	{"wlan_groups", "rest/wlangroup"}, {"firewall_rules", "rest/firewallrule"}, {"firewall_groups", "rest/firewallgroup"},
	{"port_forwards", "rest/portforward"}, {"routes", "rest/routing"}, {"settings", "rest/setting"},
	{"port_profiles", "rest/portconf"}, {"dynamic_dns", "rest/dynamicdns"}, {"radius_profiles", "rest/radiusprofile"},
	{"alarms", "stat/alarm?archived=false"}, {"rogue_aps", "stat/rogueap?within=24"}, {"events", "stat/event?within=168&_limit=1000"},
}

var v2Endpoints = [][2]string{
	{"firewall_policies", "firewall-policies"}, {"firewall_zones", "firewall/zone"}, {"traffic_rules", "trafficrules"},
	{"traffic_routes", "trafficroutes"}, {"nat_rules", "nat"}, {"devices", "device"},
	{"clients_history", "clients/history?onlyNonBlocked=false&includeUnifiDevices=false&withinHours=720"},
}

func reportQueries() [][3]any {
	now := time.Now().UnixMilli()
	d := int64(86400 * 1000)
	siteAttrs := []string{"bytes", "wan-tx_bytes", "wan-rx_bytes", "wlan_bytes", "num_sta", "lan-num_sta", "wlan-num_sta", "time"}
	return [][3]any{
		{"hourly_site", "stat/report/hourly.site", map[string]any{"attrs": siteAttrs, "start": now - 7*d, "end": now}},
		{"daily_site", "stat/report/daily.site", map[string]any{"attrs": siteAttrs, "start": now - 30*d, "end": now}},
		{"hourly_gw", "stat/report/hourly.gw", map[string]any{"attrs": []string{"time", "cpu", "mem", "wan-tx_bytes", "wan-rx_bytes", "lan-rx_bytes", "lan-tx_bytes"}, "start": now - 7*d, "end": now}},
		{"ips_events", "stat/ips/event", map[string]any{"start": now - 30*d, "end": now, "_limit": 500}},
		{"site_dpi", "stat/sitedpi", map[string]any{"type": "by_cat"}},
		{"client_dpi", "stat/stadpi", map[string]any{"type": "by_cat"}},
	}
}

// Collect reads everything the UI shows. Endpoints that don't exist on this console are logged, not fatal.
func (c *Client) Collect() map[string]any {
	started := time.Now()
	var mu sync.Mutex
	var log []map[string]any
	rec := func(source, name, path string, st int, data any) any {
		ok := st == 200
		var count any
		if ok {
			switch d := data.(type) {
			case []any:
				count = len(d)
			case map[string]any:
				if arr, ok := d["data"].([]any); ok {
					count = len(arr)
				}
			}
		}
		e := any(nil)
		if !ok {
			e = ErrText(data)
		}
		mu.Lock()
		log = append(log, map[string]any{"source": source, "name": name, "path": path, "status": st, "ok": ok, "count": count, "error": e})
		mu.Unlock()
		if ok {
			return data
		}
		return nil
	}
	integ, legacy, v2, meta := map[string]any{}, map[string]any{}, map[string]any{}, map[string]any{}
	set := func(m map[string]any, k string, v any) { mu.Lock(); m[k] = v; mu.Unlock() }

	base := "/proxy/network/integration/v1"
	st, info := c.Get(base + "/info")
	integ["info"] = rec("integration", "info", base+"/info", st, info)
	st, sitesRaw := c.pagedIntegration(base + "/sites")
	sites, _ := rec("integration", "sites", base+"/sites", st, sitesRaw).([]any)
	integ["sites"] = sites
	c.mu.RLock()
	hint := c.site
	c.mu.RUnlock()
	var site map[string]any
	for _, x := range sites {
		m, _ := x.(map[string]any)
		if m != nil && hint != "" && (m["id"] == hint || m["internalReference"] == hint || m["name"] == hint) {
			site = m
		}
	}
	if site == nil && len(sites) > 0 {
		site, _ = sites[0].(map[string]any)
	}
	legacySite := "default"
	if hint != "" {
		legacySite = hint
	}
	if site != nil {
		if r, ok := site["internalReference"].(string); ok && r != "" {
			legacySite = r
		}
	}
	c.setLegacySite(legacySite)
	meta["site"] = map[string]any{"id": get(site, "id"), "name": get(site, "name"), "legacy": legacySite}

	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	run := func(f func()) {
		wg.Add(1)
		go func() { sem <- struct{}{}; defer func() { <-sem; wg.Done() }(); f() }()
	}
	if site != nil {
		sid := fmt.Sprint(site["id"])
		for _, e := range integrationSite {
			name, ep := e[0], e[1]
			run(func() {
				p := base + "/sites/" + sid + ep
				st, d := c.pagedIntegration(p)
				set(integ, name, rec("integration", name, p, st, d))
			})
		}
	}
	for _, e := range legacyEndpoints {
		name, ep := e[0], e[1]
		run(func() {
			p := c.SitePath(ep, false)
			st, d := c.Get(p)
			if st == 200 {
				d = Data(d)
			}
			set(legacy, name, rec("legacy", name, p, st, d))
		})
	}
	for _, q := range reportQueries() {
		name, ep, body := q[0].(string), q[1].(string), q[2]
		run(func() {
			p := c.SitePath(ep, false)
			st, d := c.Request("POST", p, body)
			if st == 200 {
				d = Data(d)
			}
			set(legacy, name, rec("legacy", name, p, st, d))
		})
	}
	for _, e := range v2Endpoints {
		name, ep := e[0], e[1]
		run(func() {
			p := c.SitePath(ep, true)
			st, d := c.Get(p)
			if st == 200 {
				d = Data(d)
			}
			set(v2, name, rec("v2", name, p, st, d))
		})
	}
	run(func() {
		now := time.Now().UnixMilli()
		p := c.SitePath("system-log/all", true)
		st, d := c.Request("POST", p, map[string]any{"timestampFrom": now - 7*86400000, "timestampTo": now, "pageSize": 300, "pageNumber": 0})
		if st == 200 {
			d = Data(d)
		}
		set(v2, "system_log", rec("v2", "system_log", p, st, d))
	})
	wg.Wait()

	// per-device statistics and detail (Integration API)
	if site != nil {
		sid := fmt.Sprint(site["id"])
		stats, detail := map[string]any{}, map[string]any{}
		devs, _ := integ["devices"].([]any)
		for _, x := range devs {
			dev, _ := x.(map[string]any)
			id := fmt.Sprint(get(dev, "id"))
			run(func() {
				if st, d := c.Get(base + "/sites/" + sid + "/devices/" + id + "/statistics/latest"); st == 200 {
					set(stats, id, d)
				}
				if st, d := c.Get(base + "/sites/" + sid + "/devices/" + id); st == 200 {
					set(detail, id, d)
				}
			})
		}
		wg.Wait()
		integ["device_stats"] = stats
		integ["device_detail"] = detail
	}
	meta["host"] = c.Host()
	meta["collected_at"] = time.Now().Format("2006-01-02T15:04:05-0700")
	meta["duration_s"] = float64(time.Since(started).Milliseconds()/100) / 10
	meta["log"] = log
	return Redact(map[string]any{"integration": integ, "legacy": legacy, "v2": v2, "meta": meta}).(map[string]any)
}

func get(m map[string]any, k string) any {
	if m == nil {
		return nil
	}
	return m[k]
}

// Cache keeps the last bundle and refreshes it in the background.
type Cache struct {
	c      *Client
	mu     sync.Mutex
	bundle map[string]any
	at     time.Time
	busy   sync.Mutex
}

func NewCache(c *Client) *Cache { return &Cache{c: c} }

func (k *Cache) Age() (time.Duration, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.bundle == nil {
		return 0, false
	}
	return time.Since(k.at), true
}

func (k *Cache) Invalidate() {
	k.mu.Lock()
	k.at = time.Time{}
	k.mu.Unlock()
}

func (k *Cache) Clear() {
	k.mu.Lock()
	k.bundle, k.at = nil, time.Time{}
	k.mu.Unlock()
}

// Get returns cached data unless it's older than maxAge or fresh is requested. One collection at a time.
func (k *Cache) Get(fresh bool, maxAge time.Duration) map[string]any {
	k.mu.Lock()
	if !fresh && k.bundle != nil && time.Since(k.at) < maxAge {
		b := k.bundle
		k.mu.Unlock()
		return b
	}
	k.mu.Unlock()
	started := time.Now()
	k.busy.Lock()
	defer k.busy.Unlock()
	k.mu.Lock()
	if k.bundle != nil && k.at.After(started) {
		b := k.bundle
		k.mu.Unlock()
		return b
	}
	k.mu.Unlock()
	b := k.c.Collect()
	meta, _ := b["meta"].(map[string]any)
	meta["cached_at"] = float64(time.Now().UnixMilli()) / 1000
	k.mu.Lock()
	k.bundle, k.at = b, time.Now()
	k.mu.Unlock()
	return b
}
