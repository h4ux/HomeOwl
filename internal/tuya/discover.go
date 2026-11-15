package tuya

import (
	"context"
	"crypto/aes"
	"crypto/md5"
	"encoding/binary"
	"encoding/json"
	"net"
	"net/netip"
	"sort"
	"sync"
	"time"
)

// Found is a Tuya device seen on the network: from its broadcast (ID, version, product) or because port 6668 answers.
type Found struct {
	IP      string `json:"ip"`
	ID      string `json:"id,omitempty"`
	Version string `json:"version,omitempty"`
	Product string `json:"product,omitempty"`
	How     string `json:"how"` // "broadcast" or "port"
}

// udpKey: the fixed key Tuya devices encrypt their broadcasts with (MD5 of a published constant).
var udpKey = func() []byte { k := md5.Sum([]byte("yGAdlopoPVldABfn")); return k[:] }()

// decodeBroadcast reads a device's "I'm here" packet (3.1 plain, 3.3/3.4 ECB, 3.5 GCM).
func decodeBroadcast(b []byte) (Found, bool) {
	var js []byte
	switch {
	case len(b) > 20 && binary.BigEndian.Uint32(b) == prefix55:
		f, err := unpack55(b, nil)
		if err != nil {
			return Found{}, false
		}
		js = f.payload
		if len(js) > 0 && js[0] != '{' {
			d, err := ecb(udpKey, js[:len(js)-len(js)%aes.BlockSize], false)
			if err != nil {
				return Found{}, false
			}
			js = unpad(d)
		}
	case len(b) > 50 && binary.BigEndian.Uint32(b) == prefix66:
		f, err := unpack66(b, udpKey)
		if err != nil {
			return Found{}, false
		}
		js = f.payload
	default:
		return Found{}, false
	}
	var m struct {
		IP         string `json:"ip"`
		GwID       string `json:"gwId"`
		Version    string `json:"version"`
		ProductKey string `json:"productKey"`
	}
	if json.Unmarshal(js, &m) != nil || m.IP == "" || m.GwID == "" {
		return Found{}, false
	}
	return Found{IP: m.IP, ID: m.GwID, Version: m.Version, Product: m.ProductKey, How: "broadcast"}, true
}

// Listen collects broadcasts for a while (devices announce themselves every few seconds). It only hears devices
// on the same network as this computer; ports in use by another program are skipped.
func Listen(ctx context.Context, d time.Duration) []Found {
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	var mu sync.Mutex
	got := map[string]Found{}
	var wg sync.WaitGroup
	for _, port := range []int{6666, 6667, 7000} {
		c, err := net.ListenUDP("udp4", &net.UDPAddr{Port: port})
		if err != nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer c.Close()
			go func() { <-ctx.Done(); c.Close() }()
			buf := make([]byte, 2048)
			for {
				n, _, err := c.ReadFromUDP(buf)
				if err != nil {
					return
				}
				if f, ok := decodeBroadcast(buf[:n]); ok {
					mu.Lock()
					got[f.ID] = f
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	out := make([]Found, 0, len(got))
	for _, f := range got {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IP < out[j].IP })
	return out
}

// Probe tries port 6668 on every address of a network (up to a /22) and returns the ones that answer.
func Probe(ctx context.Context, cidr string) ([]Found, error) {
	p, err := netip.ParsePrefix(cidr)
	if err != nil {
		return nil, err
	}
	p = p.Masked()
	if !p.Addr().Is4() || p.Bits() < 22 {
		return nil, errNetTooBig
	}
	var mu sync.Mutex
	out := []Found{}
	sem := make(chan struct{}, 64)
	var wg sync.WaitGroup
	for a := p.Addr().Next(); p.Contains(a); a = a.Next() {
		if ctx.Err() != nil {
			break
		}
		ip := a.String()
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			c, err := (&net.Dialer{Timeout: 700 * time.Millisecond}).DialContext(ctx, "tcp", net.JoinHostPort(ip, "6668"))
			if err != nil {
				return
			}
			c.Close()
			mu.Lock()
			out = append(out, Found{IP: ip, How: "port"})
			mu.Unlock()
		}()
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool {
		a, _ := netip.ParseAddr(out[i].IP)
		b, _ := netip.ParseAddr(out[j].IP)
		return a.Less(b)
	})
	return out, nil
}

type tuyaErr string

func (e tuyaErr) Error() string { return string(e) }

const errNetTooBig = tuyaErr("pick a network of up to 1024 addresses (/22)")
