package secure

import (
	"bytes"
	"encoding/base64"
	"net"
	"testing"
)

func TestVaultTamperAndContext(t *testing.T) {
	v, e := NewVault(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	if e != nil {
		t.Fatal(e)
	}
	b := v.Seal([]byte("private session"), "a")
	if bytes.Contains(b, []byte("private session")) {
		t.Fatal("plaintext persisted")
	}
	if _, e = v.Open(b, "b"); e == nil {
		t.Fatal("context substitution accepted")
	}
	b[len(b)-1] ^= 1
	if _, e = v.Open(b, "a"); e == nil {
		t.Fatal("tampering accepted")
	}
}
func TestPublicDestinations(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "::1", "10.1.2.3", "169.254.169.254", "100.64.1.2", "192.168.1.2", "fd00::1", "0.0.0.0"} {
		if publicIP(net.ParseIP(s)) {
			t.Fatalf("private IP allowed: %s", s)
		}
	}
	for _, s := range []string{"http://example.com", "https://user:pass@example.com", "https://example.com:8080", "https://example.com/#token"} {
		if ValidateURL(s) == nil {
			t.Fatalf("invalid URL allowed: %s", s)
		}
	}
}
