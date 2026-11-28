package netq

import (
	"fmt"
	"strings"
)

// ParseUniFi reads UniFi's speed-test archive (stat/report/archive.speedtest): one entry per test with
// xput_download / xput_upload in Mbps, latency in ms, and time in ms (or rundate in seconds).
func ParseUniFi(list []map[string]any) []SpeedTest {
	out := []SpeedTest{}
	for _, m := range list {
		t := int64(num(m["time"]))
		if t == 0 {
			t = int64(num(m["rundate"]))
		}
		if t > 0 && t < 1e11 { // seconds
			t *= 1000
		}
		st := SpeedTest{T: t, Down: num(m["xput_download"]), Up: num(m["xput_upload"]), Latency: num(m["latency"]),
			Server: server(m["server"]), WAN: str(m["interface_name"])}
		if st.T > 0 && (st.Down > 0 || st.Up > 0) {
			out = append(out, st)
		}
	}
	return out
}

// server: UniFi gives the test server as an object (city, provider, country) or, on older versions, a name.
func server(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case map[string]any:
		var p []string
		for _, k := range []string{"provider_name", "provider", "name", "city", "country"} {
			if s := str(x[k]); s != "" && !contains(p, s) {
				p = append(p, s)
			}
		}
		return strings.Join(p, ", ")
	}
	return ""
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func num(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case string:
		var f float64
		if _, err := fmt.Sscan(x, &f); err == nil {
			return f
		}
	}
	return 0
}

func str(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}
