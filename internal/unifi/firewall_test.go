package unifi

import "testing"

func TestFwTarget(t *testing.T) {
	for ip, ok := range map[string]bool{
		"192.168.2.20": true, "192.168.2.0/24": true, "10.0.0.0/16": true,
		"192.168.2.5/24": false, // not the network's own address
		"203.0.113.0/24": false, "8.8.8.8": false, "10.0.0.0/8": false, "192.168.2.20/32": false, "": false,
	} {
		_, _, _, err := fwTarget(Action{"ip": ip, "protocol": "tcp", "ports": "6668"})
		if (err == nil) != ok {
			t.Errorf("%q: %v", ip, err)
		}
	}
}
