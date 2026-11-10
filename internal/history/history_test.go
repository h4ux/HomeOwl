package history

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/h4ux/homeowl/internal/unifi"
)

func bundle(t *testing.T, s string) map[string]any {
	t.Helper()
	var b map[string]any
	if err := json.Unmarshal([]byte(s), &b); err != nil {
		t.Fatal(err)
	}
	return unifi.Redact(b).(map[string]any) // the server only ever sees redacted bundles
}

const day1 = `{"meta":{"host":"https://192.168.1.1","site":{"name":"Default"}},"legacy":{
 "wlans":[{"_id":"w1","name":"Home","x_passphrase":"correct-horse-1","hide_ssid":false}],
 "networks":[{"_id":"n1","name":"IoT","vlan":20,"internet_access_enabled":true}],
 "known_clients":[{"_id":"c1","mac":"aa:bb:cc:00:00:01","name":"Printer","use_fixedip":true,"fixed_ip":"192.168.1.20","last_seen":100},
                  {"_id":"c2","mac":"aa:bb:cc:00:00:02","hostname":"phone","last_seen":100}],
 "devices":[{"_id":"d1","mac":"aa:bb:cc:00:00:10","name":"Hall AP","version":"6.6.1","uptime":100,"num_sta":4,
   "radio_table":[{"radio":"ng","channel":1,"tx_power_mode":"auto"},{"radio":"na","channel":36,"tx_power_mode":"auto"}]}]},
 "v2":{"firewall_policies":[{"_id":"p1","name":"Block IoT","enabled":true}]}}`

func TestRecordAndDiff(t *testing.T) {
	unifi.RedactKey = []byte("test-key")
	defer func() { unifi.RedactKey = nil }()
	st := New(t.TempDir())
	e, err := st.Record(bundle(t, day1))
	if err != nil || e == nil {
		t.Fatalf("first snapshot: %v %v", e, err)
	}
	// counters and a client nobody configured are not configuration
	s1, _ := st.Load(e.File)
	if _, ok := s1.Sections["devices"]["d1"]["uptime"]; ok {
		t.Fatal("uptime must not be kept")
	}
	if _, ok := s1.Sections["clients"]["c2"]; ok {
		t.Fatal("unnamed clients must not be kept")
	}
	if strings.Contains(mustJSON(s1), "correct-horse") {
		t.Fatal("a password reached the snapshot")
	}
	// only live data changed → no new snapshot
	same := strings.ReplaceAll(strings.ReplaceAll(day1, `"uptime":100`, `"uptime":999`), `"num_sta":4`, `"num_sta":9`)
	if e2, _ := st.Record(bundle(t, same)); e2 != nil {
		t.Fatal("live counters alone must not make a snapshot")
	}
	// same-length new password, channel change on one radio, firmware update, new policy, IoT loses internet,
	// and this time the v2 API wasn't reachable (firewall policies must not look deleted)
	day2 := strings.NewReplacer(`"correct-horse-1"`, `"correct-horse-2"`, `{"radio":"na","channel":36`, `{"radio":"na","channel":44`,
		`"version":"6.6.1"`, `"version":"6.7.0"`, `"internet_access_enabled":true`, `"internet_access_enabled":false`,
		`"v2":{"firewall_policies":[{"_id":"p1","name":"Block IoT","enabled":true}]}`, `"v2":{}`).Replace(day1)
	day2 = strings.Replace(day2, `"networks":[`, `"networks":[{"_id":"n2","name":"Guests","vlan":30},`, 1)
	e2, err := st.Record(bundle(t, day2))
	if err != nil || e2 == nil {
		t.Fatalf("second snapshot: %v %v", e2, err)
	}
	s2, _ := st.Load(e2.File)
	if _, ok := s2.Sections["firewall_policies"]["p1"]; !ok {
		t.Fatal("sections missing from a collection must carry over")
	}
	got := map[string]string{}
	for _, c := range Diff(s1, s2) {
		paths := []string{}
		for _, f := range c.Fields {
			paths = append(paths, f.Path)
		}
		got[c.Area+"/"+c.Name+"/"+c.Kind] = strings.Join(paths, ",")
	}
	want := map[string]string{
		"Wi-Fi/Home/changed":            "x_passphrase",
		"Networks & VLANs/IoT/changed":  "internet_access_enabled",
		"Networks & VLANs/Guests/added": "",
		"UniFi devices/Hall AP/changed": "radio_table[radio=na].channel,version",
	}
	for k, v := range want {
		if g, ok := got[k]; !ok || g != v {
			t.Errorf("%s: got %q (present %v), want %q", k, g, ok, v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("unexpected changes: %v", got)
	}
	if e2.Changes != 4 {
		t.Errorf("entry counts %d changes, want 4", e2.Changes)
	}
	if l := st.List(); len(l) != 2 || l[0].File == l[1].File {
		t.Fatalf("index has %d entries", len(l))
	}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
