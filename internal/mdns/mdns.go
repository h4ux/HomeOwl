// Package mdns answers multicast DNS (Bonjour) questions for a few names of our own, so phones and laptops on the
// home network can open e.g. http://hi.local without anyone typing an IP address. It only answers A (IPv4)
// questions for the names it was given, with the address of the interface the question came in on. It never
// asks questions itself and never answers for anything else; the system's own responder keeps doing its job.
package mdns

import (
	"context"
	"encoding/binary"
	"errors"
	"log"
	"net"
	"strings"
	"sync"
	"time"
)

var group = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}

const (
	typeA   = 1
	typeANY = 255
	ttl     = 120
)

// Responder answers for a set of names. The zero value is ready to use.
type Responder struct {
	mu     sync.Mutex
	names  map[string]bool // "hi.local." (lowercase, with the trailing dot)
	cancel context.CancelFunc
	err    string
	ifaces []string
}

// Set replaces the names (bare labels like "hi" or full "hi.local"); an empty list stops answering.
func (r *Responder) Set(names []string) {
	m := map[string]bool{}
	for _, n := range names {
		if fq := FQDN(n); fq != "" {
			m[fq] = true
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.names = m
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	r.err, r.ifaces = "", nil
	if len(m) == 0 {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	go r.run(ctx)
}

// Status: the interfaces it answers on, or why it can't.
func (r *Responder) Status() (ifaces []string, err string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.ifaces...), r.err
}

// FQDN turns "Hi", "hi.local" or "hi.local." into "hi.local."; "" if it isn't a valid single label.
func FQDN(n string) string {
	n = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(n)), ".")
	n = strings.TrimSuffix(n, ".local")
	if !ValidLabel(n) {
		return ""
	}
	return n + ".local."
}

// ValidLabel: 1–40 letters, digits and inner dashes.
func ValidLabel(s string) bool {
	if len(s) == 0 || len(s) > 40 || s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

func (r *Responder) has(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.names[name]
}

// run listens on every IPv4 multicast interface; each socket only answers questions from its own subnet,
// so a question seen on several sockets is answered once, with the right address.
func (r *Responder) run(ctx context.Context) {
	for ctx.Err() == nil {
		n, stop := r.listenAll(ctx)
		if n == 0 {
			stop()
			select { // no usable interface (yet): try again later
			case <-ctx.Done():
			case <-time.After(30 * time.Second):
			}
			continue
		}
		// interfaces come and go (Wi-Fi, VPN): rebuild now and then
		select {
		case <-ctx.Done():
		case <-time.After(10 * time.Minute):
		}
		stop()
	}
}

func (r *Responder) listenAll(ctx context.Context) (int, context.CancelFunc) {
	lctx, stop := context.WithCancel(ctx)
	ifs, err := net.Interfaces()
	if err != nil {
		r.setErr(err.Error(), nil)
		return 0, stop
	}
	var names []string
	var lastErr error
	for _, ifi := range ifs {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagMulticast == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		nets := ipv4Nets(&ifi)
		if len(nets) == 0 {
			continue
		}
		ifc := ifi
		conn, err := net.ListenMulticastUDP("udp4", &ifc, group)
		if err != nil {
			lastErr = err
			continue
		}
		multicastLoop(conn)
		names = append(names, ifi.Name)
		go func() { <-lctx.Done(); _ = conn.Close() }()
		go r.serve(conn, nets)
	}
	if len(names) == 0 && lastErr != nil {
		r.setErr("can't listen for .local names: "+lastErr.Error(), nil)
	} else {
		r.setErr("", names)
	}
	return len(names), stop
}

func (r *Responder) setErr(e string, ifaces []string) {
	r.mu.Lock()
	r.err, r.ifaces = e, ifaces
	r.mu.Unlock()
}

func ipv4Nets(ifi *net.Interface) []*net.IPNet {
	addrs, _ := ifi.Addrs()
	var out []*net.IPNet
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLinkLocalUnicast() {
			out = append(out, n)
		}
	}
	return out
}

func (r *Responder) serve(conn *net.UDPConn, nets []*net.IPNet) {
	buf := make([]byte, 9000)
	for {
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		var local net.IP
		for _, nt := range nets {
			if nt.Contains(src.IP) {
				local = nt.IP.To4()
			}
		}
		if local == nil {
			continue // asked on another subnet: that interface's socket answers
		}
		resp, unicast := Answer(buf[:n], local, r.has, src.Port != 5353)
		if resp == nil {
			continue
		}
		if unicast || src.Port != 5353 {
			_, _ = conn.WriteToUDP(resp, src)
		}
		if src.Port == 5353 {
			if _, err := conn.WriteToUDP(resp, group); err != nil {
				log.Printf("mdns: %v", err)
			}
		}
	}
}

// Answer builds the response to one mDNS query, or nil. legacy = a plain DNS client asking port 5353 directly
// (it wants its question and id echoed and no cache-flush bit). unicast = the asker requested a unicast reply.
func Answer(q []byte, ip net.IP, has func(string) bool, legacy bool) (resp []byte, unicast bool) {
	if len(q) < 12 || ip.To4() == nil {
		return nil, false
	}
	if q[2]&0x80 != 0 || q[2]&0x78 != 0 { // a response, or not a standard query
		return nil, false
	}
	qd := int(binary.BigEndian.Uint16(q[4:6]))
	off := 12
	var hit string
	var hitQ []byte
	for i := 0; i < qd && i < 32; i++ {
		start := off
		name, next, err := readName(q, off)
		if err != nil || next+4 > len(q) {
			return nil, false
		}
		qt, qc := binary.BigEndian.Uint16(q[next:]), binary.BigEndian.Uint16(q[next+2:])
		off = next + 4
		if (qt == typeA || qt == typeANY) && qc&0x7fff == 1 && has(name) && hit == "" {
			hit, unicast = name, qc&0x8000 != 0
			hitQ = q[start:off]
		}
	}
	if hit == "" {
		return nil, false
	}
	out := make([]byte, 12, 64)
	if legacy {
		copy(out[0:2], q[0:2])
	}
	binary.BigEndian.PutUint16(out[2:], 0x8400) // response, authoritative
	if legacy {
		binary.BigEndian.PutUint16(out[4:], 1)
	}
	binary.BigEndian.PutUint16(out[6:], 1)
	if legacy {
		out = append(out, encodeName(hit)...)
		out = append(out, hitQ[len(hitQ)-4:len(hitQ)-2]...)
		out = append(out, 0, 1)
	}
	class := uint16(0x8001) // IN, cache flush: we're the only owner of this name
	if legacy {
		class = 1
	}
	out = append(out, encodeName(hit)...)
	out = binary.BigEndian.AppendUint16(out, typeA)
	out = binary.BigEndian.AppendUint16(out, class)
	out = binary.BigEndian.AppendUint32(out, ttl)
	out = binary.BigEndian.AppendUint16(out, 4)
	out = append(out, ip.To4()...)
	return out, unicast
}

var errBadName = errors.New("bad name")

// readName reads a (possibly compressed) name at off; returns it lowercase with a trailing dot, and the offset after it.
func readName(b []byte, off int) (string, int, error) {
	var sb strings.Builder
	next, jumps := -1, 0
	for {
		if off >= len(b) {
			return "", 0, errBadName
		}
		l := int(b[off])
		switch {
		case l == 0:
			if next < 0 {
				next = off + 1
			}
			if sb.Len() == 0 {
				sb.WriteByte('.')
			}
			return strings.ToLower(sb.String()), next, nil
		case l&0xc0 == 0xc0:
			if off+1 >= len(b) || jumps > 10 {
				return "", 0, errBadName
			}
			if next < 0 {
				next = off + 2
			}
			off = int(binary.BigEndian.Uint16(b[off:]) & 0x3fff)
			jumps++
		case l&0xc0 != 0:
			return "", 0, errBadName
		default:
			if off+1+l > len(b) || sb.Len()+l > 255 {
				return "", 0, errBadName
			}
			sb.Write(b[off+1 : off+1+l])
			sb.WriteByte('.')
			off += 1 + l
		}
	}
}

func encodeName(n string) []byte {
	var out []byte
	for _, l := range strings.Split(strings.TrimSuffix(n, "."), ".") {
		out = append(out, byte(len(l)))
		out = append(out, l...)
	}
	return append(out, 0)
}
