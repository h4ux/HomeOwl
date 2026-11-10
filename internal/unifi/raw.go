package unifi

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // required by the WebSocket handshake (RFC 6455), not used for security
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/h4ux/homeowl/internal/config"
)

// Raw performs a GET and returns the body as bytes (for images such as camera snapshots), capped at max bytes.
func (c *Client) Raw(ctx context.Context, path string, max int64) (int, string, []byte, error) {
	c.mu.RLock()
	host, key, hc := c.host, c.key, c.http
	c.mu.RUnlock()
	if host == "" || key == "" || hc == nil {
		return 0, "", nil, errors.New("not connected")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, host+path, nil)
	if err != nil {
		return 0, "", nil, err
	}
	req.Header.Set("X-API-KEY", key)
	req.Header.Set("User-Agent", config.AppSlug+"/"+config.Version)
	resp, err := hc.Do(req)
	if err != nil {
		return 0, "", nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return resp.StatusCode, "", nil, err
	}
	if int64(len(b)) > max {
		return resp.StatusCode, "", nil, errors.New("response too large")
	}
	return resp.StatusCode, resp.Header.Get("Content-Type"), b, nil
}

// WSConn is a minimal client-side WebSocket (RFC 6455): enough to read the console's event streams.
type WSConn struct {
	conn net.Conn
	rd   *bufio.Reader
}

// DialWS opens a WebSocket to a console path, authenticated with the API key.
func (c *Client) DialWS(ctx context.Context, path string) (*WSConn, error) {
	c.mu.RLock()
	host, key, verify := c.host, c.key, c.verifyTLS
	c.mu.RUnlock()
	if host == "" || key == "" {
		return nil, errors.New("not connected")
	}
	u, err := url.Parse(host + path)
	if err != nil {
		return nil, err
	}
	addr := u.Host
	if u.Port() == "" {
		if u.Scheme == "http" {
			addr += ":80"
		} else {
			addr += ":443"
		}
	}
	d := &net.Dialer{Timeout: 10 * time.Second}
	var conn net.Conn
	if u.Scheme == "http" {
		conn, err = d.DialContext(ctx, "tcp", addr)
	} else {
		td := &tls.Dialer{NetDialer: d, Config: &tls.Config{InsecureSkipVerify: !verify, ServerName: u.Hostname()}} //nolint:gosec // self-signed consoles
		conn, err = td.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce)
	wsKey := base64.StdEncoding.EncodeToString(nonce)
	req := "GET " + u.RequestURI() + " HTTP/1.1\r\nHost: " + u.Host + "\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + wsKey + "\r\nSec-WebSocket-Version: 13\r\nX-API-KEY: " + key + "\r\nUser-Agent: " + config.AppSlug + "/" + config.Version + "\r\n\r\n"
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	if _, err := io.WriteString(conn, req); err != nil {
		conn.Close()
		return nil, err
	}
	rd := bufio.NewReader(conn)
	resp, err := http.ReadResponse(rd, &http.Request{Method: http.MethodGet})
	if err != nil {
		conn.Close()
		return nil, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		conn.Close()
		return nil, &WSStatusError{Status: resp.StatusCode}
	}
	h := sha1.Sum([]byte(wsKey + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11")) //nolint:gosec // RFC 6455
	if resp.Header.Get("Sec-WebSocket-Accept") != base64.StdEncoding.EncodeToString(h[:]) {
		conn.Close()
		return nil, errors.New("websocket handshake failed")
	}
	_ = conn.SetDeadline(time.Time{})
	return &WSConn{conn: conn, rd: rd}, nil
}

// WSStatusError is returned when the console answers the upgrade with a normal HTTP status (e.g. 404: no Protect).
type WSStatusError struct{ Status int }

func (e *WSStatusError) Error() string { return fmt.Sprintf("HTTP %d", e.Status) }

func (w *WSConn) Close() error { return w.conn.Close() }

// write sends one masked frame (clients must mask).
func (w *WSConn) write(op byte, payload []byte) error {
	if len(payload) > 125 {
		return errors.New("control frame too long")
	}
	mask := make([]byte, 4)
	_, _ = rand.Read(mask)
	f := []byte{0x80 | op, 0x80 | byte(len(payload))}
	f = append(f, mask...)
	for i, b := range payload {
		f = append(f, b^mask[i%4])
	}
	_ = w.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err := w.conn.Write(f)
	return err
}

// Ping sends a ping (keeps idle connections open).
func (w *WSConn) Ping() error { return w.write(0x9, nil) }

// Read returns the next text or binary message (answering pings on the way). idle is how long to wait for any frame.
func (w *WSConn) Read(idle time.Duration, max int) ([]byte, error) {
	var msg []byte
	for {
		_ = w.conn.SetReadDeadline(time.Now().Add(idle))
		var hd [2]byte
		if _, err := io.ReadFull(w.rd, hd[:]); err != nil {
			return nil, err
		}
		fin, op, masked := hd[0]&0x80 != 0, hd[0]&0x0f, hd[1]&0x80 != 0
		n := uint64(hd[1] & 0x7f)
		switch n {
		case 126:
			var b [2]byte
			if _, err := io.ReadFull(w.rd, b[:]); err != nil {
				return nil, err
			}
			n = uint64(b[0])<<8 | uint64(b[1])
		case 127:
			var b [8]byte
			if _, err := io.ReadFull(w.rd, b[:]); err != nil {
				return nil, err
			}
			n = 0
			for _, x := range b {
				n = n<<8 | uint64(x)
			}
		}
		if n > uint64(max) || uint64(len(msg))+n > uint64(max) {
			return nil, errors.New("websocket message too large")
		}
		var mask [4]byte
		if masked {
			if _, err := io.ReadFull(w.rd, mask[:]); err != nil {
				return nil, err
			}
		}
		p := make([]byte, n)
		if _, err := io.ReadFull(w.rd, p); err != nil {
			return nil, err
		}
		if masked {
			for i := range p {
				p[i] ^= mask[i%4]
			}
		}
		switch op {
		case 0x8: // close
			_ = w.write(0x8, nil)
			return nil, io.EOF
		case 0x9: // ping
			if err := w.write(0xA, p); err != nil {
				return nil, err
			}
			continue
		case 0xA: // pong
			continue
		case 0x0, 0x1, 0x2:
			msg = append(msg, p...)
			if fin {
				return msg, nil
			}
		default:
			return nil, fmt.Errorf("unknown websocket opcode %d", op)
		}
	}
}

// IsWSNotFound reports whether a dial failed because the console has no such endpoint.
func IsWSNotFound(err error) bool {
	var se *WSStatusError
	return errors.As(err, &se) && (se.Status == 404 || se.Status == 502 || se.Status == 503)
}
