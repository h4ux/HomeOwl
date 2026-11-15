package server

import "testing"

func TestDetectedKinds(t *testing.T) {
	for in, want := range map[string]string{"AABBCC000010": "aa:bb:cc:00:00:10", "aa-bb-cc-00-00-10": "aa:bb:cc:00:00:10", "aa:bb:cc:00:00:1": "", "zz": "", "": ""} {
		if got := normMAC(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
	a := checkinApp(t)
	detected.Lock()
	detected.kinds = nil
	detected.Unlock()
	t.Cleanup(func() { detected.Lock(); detected.kinds = nil; detected.Unlock() })
	a.tuya.Devices = []tuyaDev{{MAC: "aa:bb:cc:00:00:31"}, {MAC: "aa:bb:cc:00:00:32"}}
	a.tuya.Devices[0].Category, a.tuya.Devices[1].Category = "cz", "dj"
	k := a.detectedKinds()
	if k["aa:bb:cc:00:00:31"] != "plug" || k["aa:bb:cc:00:00:32"] != "bulb" {
		t.Fatalf("tuya: %v", k)
	}
}
