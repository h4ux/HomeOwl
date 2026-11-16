package mdns

import (
	"context"
	"encoding/binary"
	"net"
	"strings"
	"time"
)

// Apple devices say what they are over multicast DNS: Macs in _device-info._tcp ("model=Mac16,10"), iPhones, iPads
// and Macs in _companion-link._tcp ("rpMd=iPhone17,1"), AirPlay receivers in _airplay._tcp ("model=…"). Model asks
// one device directly (a "legacy unicast" query to its port 5353, answered to the asking port), so nothing is sent
// to the whole network. It returns "" when the device says nothing, isn't reachable, or isn't an Apple device.

const (
	typePTR = 12
	typeTXT = 16
)

var modelServices = []string{"_device-info._tcp.local.", "_companion-link._tcp.local.", "_airplay._tcp.local."}

// Model asks the device at ip for its model identifier, waiting at most until ctx ends.
func Model(ctx context.Context, ip net.IP) string {
	if ip == nil || ip.To4() == nil && ip.To16() == nil {
		return ""
	}
	conn, err := net.ListenUDP("udp", nil)
	if err != nil {
		return ""
	}
	defer conn.Close()
	to := &net.UDPAddr{IP: ip, Port: 5353}
	if _, err := conn.WriteToUDP(modelQuery(typePTR, modelServices...), to); err != nil {
		return ""
	}
	if d, ok := ctx.Deadline(); ok {
		_ = conn.SetReadDeadline(d)
	} else {
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	}
	asked := map[string]bool{}
	buf := make([]byte, 9000)
	for {
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			return ""
		}
		if !from.IP.Equal(ip) {
			continue
		}
		model, instances := ParseModel(buf[:n])
		if model != "" {
			return model
		}
		var ask []string // services listed without their TXT: ask for those
		for _, in := range instances {
			if !asked[in] {
				asked[in] = true
				ask = append(ask, in)
			}
		}
		if len(ask) > 0 {
			_, _ = conn.WriteToUDP(modelQuery(typeTXT, ask...), to)
		}
	}
}

// modelQuery: one question per name, asking for a unicast answer.
func modelQuery(qtype uint16, names ...string) []byte {
	b := make([]byte, 12)
	binary.BigEndian.PutUint16(b[4:], uint16(len(names)))
	for _, n := range names {
		b = append(b, encodeName(n)...)
		b = binary.BigEndian.AppendUint16(b, qtype)
		b = binary.BigEndian.AppendUint16(b, 0x8001) // IN, unicast response
	}
	return b
}

// ParseModel reads an mDNS answer: the model identifier from a TXT record, or else the service instances it named
// (PTR) whose TXT records weren't included.
func ParseModel(b []byte) (model string, instances []string) {
	if len(b) < 12 || b[2]&0x80 == 0 { // not a response
		return "", nil
	}
	qd := int(binary.BigEndian.Uint16(b[4:]))
	rr := int(binary.BigEndian.Uint16(b[6:])) + int(binary.BigEndian.Uint16(b[8:])) + int(binary.BigEndian.Uint16(b[10:]))
	off := 12
	for i := 0; i < qd; i++ {
		_, next, err := readName(b, off)
		if err != nil || next+4 > len(b) {
			return "", nil
		}
		off = next + 4
	}
	txt := map[string]bool{}
	var ptrs []string
	for i := 0; i < rr; i++ {
		name, next, err := readName(b, off)
		if err != nil || next+10 > len(b) {
			break
		}
		typ := binary.BigEndian.Uint16(b[next:])
		l := int(binary.BigEndian.Uint16(b[next+8:]))
		data := next + 10
		if data+l > len(b) {
			break
		}
		switch typ {
		case typeTXT:
			txt[name] = true
			for p := data; p < data+l; {
				sl := int(b[p])
				if p+1+sl > data+l {
					break
				}
				kv := string(b[p+1 : p+1+sl])
				p += 1 + sl
				k, v, ok := strings.Cut(kv, "=")
				if ok && (strings.EqualFold(k, "model") || k == "rpMd") && v != "" && model == "" {
					model = v
				}
			}
		case typePTR:
			if in, _, err := readName(b, data); err == nil {
				ptrs = append(ptrs, in)
			}
		}
		off = data + l
	}
	if model != "" {
		return model, nil
	}
	for _, p := range ptrs {
		if !txt[p] {
			instances = append(instances, p)
		}
	}
	return "", instances
}
