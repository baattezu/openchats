package secure

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

func ID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func Token() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func Hash(s string) string { v := sha256.Sum256([]byte(s)); return hex.EncodeToString(v[:]) }

type Vault struct{ aead cipher.AEAD }

func NewVault(key string) (*Vault, error) {
	b, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(b) != 32 {
		return nil, errors.New("ENCRYPTION_KEY must be base64 encoding of 32 random bytes")
	}
	block, err := aes.NewCipher(b)
	if err != nil {
		return nil, err
	}
	a, err := cipher.NewGCM(block)
	return &Vault{a}, err
}
func (v *Vault) Seal(data []byte, context string) []byte {
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic(err)
	}
	return v.aead.Seal(nonce, nonce, data, []byte(context))
}
func (v *Vault) Open(data []byte, context string) ([]byte, error) {
	n := v.aead.NonceSize()
	if len(data) < n {
		return nil, errors.New("invalid encrypted data")
	}
	return v.aead.Open(nil, data[:n], data[n:], []byte(context))
}

func publicIP(ip net.IP) bool {
	return ip != nil && ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() && !ip.IsUnspecified() && !net.ParseIP("100.64.0.0").Equal(ip) && !(&net.IPNet{IP: net.ParseIP("100.64.0.0"), Mask: net.CIDRMask(10, 32)}).Contains(ip)
}
func ValidateURL(raw string) error {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return errors.New("a public HTTPS URL without credentials or fragment is required")
	}
	if u.Port() != "" && u.Port() != "443" {
		return errors.New("only HTTPS port 443 is allowed")
	}
	return nil
}

// Resolve at dial time and connect directly to the validated IP: DNS rebinding cannot
// swap a validated hostname for an internal address between validation and use.
func PublicClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }, Transport: &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(addr)
		if e != nil {
			return nil, e
		}
		if port != "443" {
			return nil, errors.New("port blocked")
		}
		ips, e := net.DefaultResolver.LookupIP(ctx, "ip", host)
		if e != nil {
			return nil, e
		}
		if len(ips) == 0 {
			return nil, errors.New("no addresses")
		}
		for _, ip := range ips {
			if !publicIP(ip) {
				return nil, errors.New("private destination blocked")
			}
		}
		return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
	}}}
}
func ReadLimited(r io.Reader, max int64) ([]byte, error) {
	b, e := io.ReadAll(io.LimitReader(r, max+1))
	if e != nil {
		return nil, e
	}
	if int64(len(b)) > max {
		return nil, errors.New("response too large")
	}
	return b, nil
}
