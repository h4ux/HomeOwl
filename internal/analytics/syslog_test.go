package analytics

import "testing"

func TestParseSyslogCEF(t *testing.T) {
	line := "<11>Nov 10 10:00:04 GW CEF:0|Ubiquiti|UniFi Network|9.3.45|201|Threat Detected and Blocked|7|proto=TCP src=81.181.129.172 spt=4455 dst=203.0.113.47 dpt=445 UNIFIcategory=Security UNIFIsubCategory=Intrusion Prevention act=blocked msg=ET SCAN Suspicious inbound to SMB"
	r := ParseSyslog(line, "192.168.1.1")
	if r["kind"] != "cef" || r["src"] != "81.181.129.172" || r["dport"] != 445 || r["action"] != "blocked" {
		t.Fatalf("bad parse: %#v", r)
	}
	if r["category"] != "Security/Intrusion Prevention" || r["msg"] != "ET SCAN Suspicious inbound to SMB" {
		t.Fatalf("bad category/msg: %#v", r)
	}
}

func TestParseSyslogFirewall(t *testing.T) {
	line := "<4>Nov 10 10:00:00 GW kernel: [WAN_IN-D-2000] IN=eth8 OUT= MAC=aa SRC=45.1.2.3 DST=203.0.113.47 LEN=40 PROTO=TCP SPT=51515 DPT=23"
	r := ParseSyslog(line, "192.168.1.1")
	if r["kind"] != "firewall" || r["action"] != "blocked" || r["rule"] != "WAN_IN-D-2000" || r["dport"] != 23 || r["iface_in"] != "eth8" {
		t.Fatalf("bad parse: %#v", r)
	}
}

func TestWeekdayCounts(t *testing.T) {
	// Monday 2026-11-02 … Sunday 2026-11-08: one of each
	got := weekdayCounts(parse(t, "2026-11-02"), parse(t, "2026-11-08"))
	for d := 1; d <= 7; d++ {
		if got[string(rune('0'+d))] != 1 {
			t.Fatalf("weekday %d: %v", d, got)
		}
	}
}
