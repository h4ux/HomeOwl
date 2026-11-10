package server

import (
	"testing"
	"time"
)

func devBundle(version, to string, up bool) map[string]any {
	return map[string]any{"legacy": map[string]any{"devices": []any{
		map[string]any{"_id": "d1", "mac": "AA:BB:CC:00:00:10", "name": "Hall AP", "version": version, "upgradable": up, "upgrade_to_firmware": to},
		map[string]any{"_id": "d2", "mac": "aa:bb:cc:00:00:11", "name": "Switch", "version": "7.1.26", "upgradable": false},
	}}}
}

func TestFirmwareWatch(t *testing.T) {
	a, err := New(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	a.firmware.Observe(devBundle("6.6.1", "6.7.0", true))
	p := a.firmware.Pending()
	first := p["aa:bb:cc:00:00:10"]
	if len(p) != 1 || first.To != "6.7.0" || first.Since == 0 {
		t.Fatalf("pending: %+v", p)
	}
	time.Sleep(1100 * time.Millisecond)
	a.firmware.Observe(devBundle("6.6.1", "6.7.0", true))
	if got := a.firmware.Pending()["aa:bb:cc:00:00:10"]; got.Since != first.Since {
		t.Fatal("the same waiting update must keep its first-seen time")
	}
	a.firmware.Observe(devBundle("6.6.1", "6.8.0", true))
	if got := a.firmware.Pending()["aa:bb:cc:00:00:10"]; got.To != "6.8.0" || got.Since == first.Since {
		t.Fatalf("a newer update starts a new wait: %+v", got)
	}
	a.firmware.Observe(devBundle("6.8.0", "", false))
	if len(a.firmware.Pending()) != 0 {
		t.Fatal("updated devices are no longer waiting")
	}

	// firmware changes come out of the configuration history
	if _, err := a.History.Record(devBundle("6.6.1", "", false)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.History.Record(devBundle("6.8.0", "", false)); err != nil {
		t.Fatal(err)
	}
	r := a.recentFirmware(30)
	if len(r) != 1 || r[0].Name != "Hall AP" || r[0].Before != "6.6.1" || r[0].After != "6.8.0" {
		t.Fatalf("recent firmware: %+v", r)
	}
}
