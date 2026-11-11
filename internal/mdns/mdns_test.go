package mdns

import (
	"encoding/binary"
	"net"
	"testing"
)

func query(name string, qtype, qclass uint16, id uint16) []byte {
	b := make([]byte, 12)
	binary.BigEndian.PutUint16(b[0:], id)
	binary.BigEndian.PutUint16(b[4:], 1)
	b = append(b, encodeName(name)...)
	b = binary.BigEndian.AppendUint16(b, qtype)
	return binary.BigEndian.AppendUint16(b, qclass)
}

func TestAnswer(t *testing.T) {
	has := func(n string) bool { return n == "hi.local." }
	ip := net.ParseIP("192.168.1.20")
	resp, uni := Answer(query("Hi.Local.", typeA, 0x8001, 7), ip, has, false)
	if resp == nil || !uni {
		t.Fatalf("want a unicast-requested answer, got %v %v", resp, uni)
	}
	if binary.BigEndian.Uint16(resp[0:]) != 0 || binary.BigEndian.Uint16(resp[6:]) != 1 || binary.BigEndian.Uint16(resp[4:]) != 0 {
		t.Fatalf("header: % x", resp[:12])
	}
	name, off, err := readName(resp, 12)
	if err != nil || name != "hi.local." {
		t.Fatalf("answer name %q %v", name, err)
	}
	if got := net.IP(resp[off+10 : off+14]); !got.Equal(ip) {
		t.Fatalf("address %v", got)
	}
	if r, _ := Answer(query("other.local.", typeA, 1, 1), ip, has, false); r != nil {
		t.Fatal("must not answer for other names")
	}
	if r, _ := Answer(query("hi.local.", 28, 1, 1), ip, has, false); r != nil {
		t.Fatal("no AAAA answers")
	}
	// a plain DNS client asking 5353 directly gets its id and question back
	leg, _ := Answer(query("hi.local.", typeA, 1, 0x1234), ip, has, true)
	if leg == nil || binary.BigEndian.Uint16(leg[0:]) != 0x1234 || binary.BigEndian.Uint16(leg[4:]) != 1 {
		t.Fatalf("legacy: % x", leg)
	}
	// responses and garbage are ignored
	resp[2] |= 0x80
	if r, _ := Answer(resp, ip, has, false); r != nil {
		t.Fatal("answered a response")
	}
	if r, _ := Answer([]byte{1, 2, 3}, ip, has, false); r != nil {
		t.Fatal("answered garbage")
	}
	loop := query("hi.local.", typeA, 1, 1)
	loop[12] = 0xc0
	loop[13] = 12 // a pointer to itself
	if r, _ := Answer(loop, ip, has, false); r != nil {
		t.Fatal("answered a looping name")
	}
}

func TestFQDN(t *testing.T) {
	for in, want := range map[string]string{"hi": "hi.local.", "Wifi.local": "wifi.local.", "guest-wifi.local.": "guest-wifi.local.", "a b": "", "-x": "", "x.y": "", "": ""} {
		if got := FQDN(in); got != want {
			t.Errorf("FQDN(%q) = %q, want %q", in, got, want)
		}
	}
}
