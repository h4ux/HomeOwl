package mdns

import (
	"context"
	"encoding/binary"
	"net"
	"os"
	"testing"
	"time"
)

func rrBytes(name string, typ uint16, data []byte) []byte {
	b := encodeName(name)
	b = binary.BigEndian.AppendUint16(b, typ)
	b = binary.BigEndian.AppendUint16(b, 1)
	b = append(b, 0, 0, 0, 120)
	b = binary.BigEndian.AppendUint16(b, uint16(len(data)))
	return append(b, data...)
}

func txtData(kv ...string) []byte {
	var d []byte
	for _, s := range kv {
		d = append(d, byte(len(s)))
		d = append(d, s...)
	}
	return d
}

func response(rrs ...[]byte) []byte {
	b := make([]byte, 12)
	b[2] = 0x84
	binary.BigEndian.PutUint16(b[6:], uint16(len(rrs)))
	for _, r := range rrs {
		b = append(b, r...)
	}
	return b
}

func TestParseModel(t *testing.T) {
	inst := "Alex's iPhone._companion-link._tcp.local."
	// PTR plus its TXT: the model
	m, in := ParseModel(response(rrBytes("_companion-link._tcp.local.", typePTR, encodeName(inst)),
		rrBytes(inst, typeTXT, txtData("rpFl=0x20000", "rpMd=iPhone17,1", "rpVr=600.1"))))
	if m != "iPhone17,1" || in != nil {
		t.Fatalf("companion-link: %q %v", m, in)
	}
	m, _ = ParseModel(response(rrBytes("Office Mac._device-info._tcp.local.", typeTXT, txtData("model=Mac16,10", "osxvers=25"))))
	if m != "Mac16,10" {
		t.Fatalf("device-info: %q", m)
	}
	// only the PTR: ask for that instance next
	m, in = ParseModel(response(rrBytes("_companion-link._tcp.local.", typePTR, encodeName(inst))))
	if m != "" || len(in) != 1 || in[0] != "alex's iphone._companion-link._tcp.local." {
		t.Fatalf("ptr only: %q %v", m, in)
	}
	// a query, junk, a truncated answer: nothing, no panic
	for _, b := range [][]byte{modelQuery(typePTR, modelServices...), {1, 2, 3}, response(rrBytes("x.local.", typeTXT, txtData("model=Mac1,1")))[:30]} {
		if m, _ := ParseModel(b); m != "" {
			t.Fatalf("junk gave %q", m)
		}
	}
}

// HOMEOWL_MDNS_IP=<an Apple device's address> go test -run TestModelLive ./internal/mdns
func TestModelLive(t *testing.T) {
	ip := os.Getenv("HOMEOWL_MDNS_IP")
	if ip == "" {
		t.Skip("set HOMEOWL_MDNS_IP")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	t.Logf("model: %q", Model(ctx, net.ParseIP(ip)))
}
