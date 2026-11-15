package tuya

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"net"
	"strconv"
	"time"
)

// The local protocol (port 6668): framed messages, AES with the device's 16-character local key.
//   3.3 — 55AA frames, AES-ECB, CRC32.
//   3.4 — 55AA frames, a session key agreed first, AES-ECB, HMAC-SHA256.
//   3.5 — 6699 frames, a session key agreed first, AES-GCM.

const (
	cmdSessStart  = 3
	cmdSessRes    = 4
	cmdSessFinish = 5
	cmdControl    = 7
	cmdDPQuery    = 10
	cmdControlNew = 13
	cmdDPQueryNew = 16

	prefix55 = 0x000055AA
	suffix55 = 0x0000AA55
	prefix66 = 0x00006699
	suffix66 = 0x00009966
)

func pkcs7(b []byte) []byte {
	n := aes.BlockSize - len(b)%aes.BlockSize
	return append(b, bytes.Repeat([]byte{byte(n)}, n)...)
}

func unpad(b []byte) []byte {
	if len(b) == 0 {
		return b
	}
	n := int(b[len(b)-1])
	if n < 1 || n > aes.BlockSize || n > len(b) {
		return b
	}
	return b[:len(b)-n]
}

func ecb(key, b []byte, enc bool) ([]byte, error) {
	c, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(b)%aes.BlockSize != 0 {
		return nil, errors.New("tuya: data isn't whole AES blocks")
	}
	out := make([]byte, len(b))
	for i := 0; i < len(b); i += aes.BlockSize {
		if enc {
			c.Encrypt(out[i:i+aes.BlockSize], b[i:i+aes.BlockSize])
		} else {
			c.Decrypt(out[i:i+aes.BlockSize], b[i:i+aes.BlockSize])
		}
	}
	return out, nil
}

func hmac256(key, b []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(b)
	return m.Sum(nil)
}

// frame is one message: its command, sequence number and (encrypted) payload.
type frame struct {
	seq, cmd uint32
	payload  []byte
	ret      uint32 // device → client: the return code
	hasRet   bool
}

// pack55 builds a 3.3/3.4 frame; with hmacKey the trailer is an HMAC (3.4), otherwise a CRC32.
func pack55(f frame, hmacKey []byte) []byte {
	tail := 4
	if hmacKey != nil {
		tail = 32
	}
	b := new(bytes.Buffer)
	_ = binary.Write(b, binary.BigEndian, []uint32{prefix55, f.seq, f.cmd, uint32(len(f.payload) + tail + 4)})
	b.Write(f.payload)
	if hmacKey != nil {
		b.Write(hmac256(hmacKey, b.Bytes()))
	} else {
		_ = binary.Write(b, binary.BigEndian, crc32.ChecksumIEEE(b.Bytes()))
	}
	_ = binary.Write(b, binary.BigEndian, uint32(suffix55))
	return b.Bytes()
}

// unpack55 reads a 3.3/3.4 frame from a device (with its return code), checking the trailer.
func unpack55(b []byte, hmacKey []byte) (frame, error) {
	var f frame
	if len(b) < 24 || binary.BigEndian.Uint32(b) != prefix55 {
		return f, errors.New("tuya: not a frame")
	}
	f.seq, f.cmd = binary.BigEndian.Uint32(b[4:]), binary.BigEndian.Uint32(b[8:])
	n := int(binary.BigEndian.Uint32(b[12:]))
	if 16+n > len(b) || n < 8 {
		return f, errors.New("tuya: short frame")
	}
	tail := 4
	if hmacKey != nil {
		tail = 32
	}
	body := b[16 : 16+n-tail-4]
	sum := b[16+n-tail-4 : 16+n-4]
	if hmacKey != nil {
		if !hmac.Equal(sum, hmac256(hmacKey, b[:16+n-tail-4])) {
			return f, errors.New("tuya: the reply doesn't match the local key")
		}
	} else if binary.BigEndian.Uint32(sum) != crc32.ChecksumIEEE(b[:16+n-tail-4]) {
		return f, errors.New("tuya: damaged reply")
	}
	if len(body) >= 4 && body[0] == 0 && body[1] == 0 { // the return code (a payload starts with '{', '3' or ciphertext)
		f.ret, f.hasRet, body = binary.BigEndian.Uint32(body), true, body[4:]
	}
	f.payload = body
	return f, nil
}

// pack66 builds a 3.5 frame: AES-GCM with the header as additional data.
func pack66(f frame, key []byte) ([]byte, error) {
	iv := make([]byte, 12)
	_, _ = rand.Read(iv)
	blk, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	g, _ := cipher.NewGCM(blk)
	hdr := new(bytes.Buffer)
	_ = binary.Write(hdr, binary.BigEndian, uint32(prefix66))
	_ = binary.Write(hdr, binary.BigEndian, uint16(0))
	_ = binary.Write(hdr, binary.BigEndian, []uint32{f.seq, f.cmd, uint32(12 + len(f.payload) + 16)})
	sealed := g.Seal(nil, iv, f.payload, hdr.Bytes()[4:])
	out := append(hdr.Bytes(), iv...)
	out = append(out, sealed...)
	return binary.BigEndian.AppendUint32(out, suffix66), nil
}

func unpack66(b []byte, key []byte) (frame, error) {
	var f frame
	if len(b) < 18+12+16+4 || binary.BigEndian.Uint32(b) != prefix66 {
		return f, errors.New("tuya: not a frame")
	}
	f.seq, f.cmd = binary.BigEndian.Uint32(b[6:]), binary.BigEndian.Uint32(b[10:])
	n := int(binary.BigEndian.Uint32(b[14:]))
	if 18+n+4 > len(b) || n < 28 {
		return f, errors.New("tuya: short frame")
	}
	blk, err := aes.NewCipher(key)
	if err != nil {
		return f, err
	}
	g, _ := cipher.NewGCM(blk)
	plain, err := g.Open(nil, b[18:30], b[30:18+n], b[4:18])
	if err != nil {
		return f, errors.New("tuya: the reply doesn't match the local key")
	}
	if len(plain) >= 4 && plain[0] == 0 && plain[1] == 0 {
		f.ret, f.hasRet, plain = binary.BigEndian.Uint32(plain), true, plain[4:]
	}
	f.payload = plain
	return f, nil
}

var localPort = "6668" // tests use another

// Local is one connection to a device on the home network.
type Local struct {
	ID, Key, Version string
	conn             net.Conn
	sess             []byte // 3.4/3.5 session key
	seq              uint32
}

// DialLocal connects to a device and, for 3.4/3.5, agrees on a session key.
func DialLocal(ip, id, key, version string, timeout time.Duration) (*Local, error) {
	if len(key) != 16 {
		return nil, errors.New("a Tuya local key is 16 characters")
	}
	switch version {
	case "", "3.3", "3.4", "3.5":
	default:
		return nil, fmt.Errorf("Tuya protocol %s isn't supported (3.3, 3.4 and 3.5 are)", version)
	}
	if version == "" {
		version = "3.3"
	}
	c, err := net.DialTimeout("tcp", net.JoinHostPort(ip, localPort), timeout)
	if err != nil {
		return nil, err
	}
	_ = c.SetDeadline(time.Now().Add(timeout))
	l := &Local{ID: id, Key: key, Version: version, conn: c}
	if version != "3.3" {
		if err := l.negotiate(); err != nil {
			c.Close()
			return nil, err
		}
	}
	return l, nil
}

func (l *Local) Close() error { return l.conn.Close() }

func (l *Local) key() []byte {
	if l.sess != nil {
		return l.sess
	}
	return []byte(l.Key)
}

func (l *Local) send(cmd uint32, payload []byte) error {
	l.seq++
	var b []byte
	var err error
	switch l.Version {
	case "3.5":
		b, err = pack66(frame{seq: l.seq, cmd: cmd, payload: payload}, l.key())
	case "3.4":
		b = pack55(frame{seq: l.seq, cmd: cmd, payload: payload}, l.key())
	default:
		b = pack55(frame{seq: l.seq, cmd: cmd, payload: payload}, nil)
	}
	if err != nil {
		return err
	}
	_, err = l.conn.Write(b)
	return err
}

func (l *Local) recv() (frame, error) {
	hdr := make([]byte, 16)
	if _, err := io.ReadFull(l.conn, hdr); err != nil {
		return frame{}, err
	}
	switch binary.BigEndian.Uint32(hdr) {
	case prefix55:
		n := int(binary.BigEndian.Uint32(hdr[12:]))
		if n > 1<<16 {
			return frame{}, errors.New("tuya: frame too big")
		}
		rest := make([]byte, n)
		if _, err := io.ReadFull(l.conn, rest); err != nil {
			return frame{}, err
		}
		var hk []byte
		if l.Version == "3.4" {
			hk = l.key()
		}
		return unpack55(append(hdr, rest...), hk)
	case prefix66:
		more := make([]byte, 2) // the 6699 header is 18 bytes
		if _, err := io.ReadFull(l.conn, more); err != nil {
			return frame{}, err
		}
		h := append(hdr, more...)
		n := int(binary.BigEndian.Uint32(h[14:]))
		if n > 1<<16 {
			return frame{}, errors.New("tuya: frame too big")
		}
		rest := make([]byte, n+4)
		if _, err := io.ReadFull(l.conn, rest); err != nil {
			return frame{}, err
		}
		return unpack66(append(h, rest...), l.key())
	}
	return frame{}, errors.New("tuya: unexpected data")
}

// negotiate: the 3.4/3.5 handshake. Each side sends a random nonce; the session key comes from both.
func (l *Local) negotiate() error {
	local := make([]byte, 16)
	_, _ = rand.Read(local)
	lk := []byte(l.Key)
	p := local
	if l.Version == "3.4" {
		p, _ = ecb(lk, local, true)
	}
	if err := l.send(cmdSessStart, p); err != nil {
		return err
	}
	f, err := l.recv()
	if err != nil {
		return fmt.Errorf("the device didn't answer the handshake: %w", err)
	}
	body := f.payload
	if l.Version == "3.4" {
		if body, err = ecb(lk, body, false); err != nil {
			return err
		}
	}
	if f.cmd != cmdSessRes || len(body) < 48 {
		return errors.New("the device refused the handshake (wrong local key?)")
	}
	remote := body[:16]
	if !hmac.Equal(body[16:48], hmac256(lk, local)) {
		return errors.New("the device has another local key")
	}
	fin := hmac256(lk, remote)
	if l.Version == "3.4" {
		fin, _ = ecb(lk, fin, true)
	}
	if err := l.send(cmdSessFinish, fin); err != nil {
		return err
	}
	x := make([]byte, 16)
	for i := range x {
		x[i] = local[i] ^ remote[i]
	}
	if l.Version == "3.4" {
		l.sess, _ = ecb(lk, x, true)
	} else {
		blk, _ := aes.NewCipher(lk)
		g, _ := cipher.NewGCM(blk)
		l.sess = g.Seal(nil, local[:12], x, nil)[:16]
	}
	return nil
}

// wrap: the JSON payload as the version expects it.
func (l *Local) wrap(cmd uint32, js []byte) ([]byte, error) {
	switch l.Version {
	case "3.3":
		enc, err := ecb(l.key(), pkcs7(js), true)
		if err != nil {
			return nil, err
		}
		if cmd == cmdDPQuery {
			return enc, nil
		}
		return append(append([]byte("3.3"), make([]byte, 12)...), enc...), nil
	case "3.4":
		if cmd != cmdDPQueryNew {
			js = append(append([]byte("3.4"), make([]byte, 12)...), js...)
		}
		return ecb(l.key(), pkcs7(js), true)
	default: // 3.5: GCM does the encryption in the frame
		if cmd != cmdDPQueryNew {
			js = append(append([]byte("3.5"), make([]byte, 12)...), js...)
		}
		return js, nil
	}
}

// unwrap: the JSON a device sent back.
func (l *Local) unwrap(b []byte) []byte {
	if l.Version == "3.3" && len(b) > 15 && string(b[:3]) == "3.3" {
		b = b[15:]
	}
	if l.Version != "3.5" && len(b) > 0 && b[0] != '{' {
		if d, err := ecb(l.key(), b[:len(b)-len(b)%aes.BlockSize], false); err == nil {
			b = unpad(d)
		}
	}
	if len(b) > 15 && (string(b[:3]) == "3.4" || string(b[:3]) == "3.5" || string(b[:3]) == "3.3") {
		b = b[15:]
	}
	return b
}

// readDPS waits for a reply that carries data points.
func (l *Local) readDPS() (map[string]any, error) {
	for i := 0; i < 4; i++ {
		f, err := l.recv()
		if err != nil {
			return nil, err
		}
		js := l.unwrap(f.payload)
		if len(js) == 0 {
			continue
		}
		var r struct {
			DPS  map[string]any `json:"dps"`
			Data struct {
				DPS map[string]any `json:"dps"`
			} `json:"data"`
		}
		if err := json.Unmarshal(js, &r); err != nil {
			if bytes.Contains(js, []byte("data unvalid")) || bytes.Contains(js, []byte("json obj data unvalid")) {
				return nil, errors.New("the device didn't accept the request (try another protocol version)")
			}
			continue
		}
		if r.DPS == nil {
			r.DPS = r.Data.DPS
		}
		if r.DPS != nil {
			return r.DPS, nil
		}
	}
	return nil, errors.New("the device sent no status")
}

// Status reads every data point.
func (l *Local) Status() (map[string]any, error) {
	now := strconv.FormatInt(time.Now().Unix(), 10)
	cmd := uint32(cmdDPQuery)
	js, _ := json.Marshal(map[string]any{"gwId": l.ID, "devId": l.ID, "uid": l.ID, "t": now})
	if l.Version != "3.3" {
		cmd, js = cmdDPQueryNew, []byte("{}")
	}
	p, err := l.wrap(cmd, js)
	if err != nil {
		return nil, err
	}
	if err := l.send(cmd, p); err != nil {
		return nil, err
	}
	return l.readDPS()
}

// Set changes one data point ("1": true turns a plug on).
func (l *Local) Set(dp string, value any) error {
	now := time.Now().Unix()
	cmd := uint32(cmdControl)
	js, _ := json.Marshal(map[string]any{"devId": l.ID, "uid": l.ID, "t": strconv.FormatInt(now, 10), "dps": map[string]any{dp: value}})
	if l.Version != "3.3" {
		cmd = cmdControlNew
		js, _ = json.Marshal(map[string]any{"protocol": 5, "t": now, "data": map[string]any{"dps": map[string]any{dp: value}}})
	}
	p, err := l.wrap(cmd, js)
	if err != nil {
		return err
	}
	if err := l.send(cmd, p); err != nil {
		return err
	}
	_, _ = l.readDPS() // the device usually echoes the new state; not every one does
	return nil
}

// LocalStatus: connect, read the data points, close.
func LocalStatus(ip, id, key, version string) (map[string]any, error) {
	l, err := DialLocal(ip, id, key, version, 5*time.Second)
	if err != nil {
		return nil, err
	}
	defer l.Close()
	return l.Status()
}

// LocalSet: connect, set one data point, close.
func LocalSet(ip, id, key, version, dp string, value any) error {
	l, err := DialLocal(ip, id, key, version, 5*time.Second)
	if err != nil {
		return err
	}
	defer l.Close()
	return l.Set(dp, value)
}
