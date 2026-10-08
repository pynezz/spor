package manifest

import (
	"bytes"
	"crypto/ed25519"
	"strings"
	"testing"
)

func TestSignParseVerify(t *testing.T) {
	pubF, _, id, err := GenerateKey("test")
	if err != nil {
		t.Fatal(err)
	}
	keys, err := ParsePublicKeys(pubF)
	if err != nil || keys[id] == nil {
		t.Fatalf("public keys: %v", err)
	}
	_, priv, _ := ed25519.GenerateKey(nil)
	keys[KeyID(priv.Public().(ed25519.PublicKey))] = priv.Public().(ed25519.PublicKey)
	h := strings.Repeat("ab", 32)
	sig, err := Sign(priv, []Entry{{"track.mp3", h}, {"cover.jpg", h}})
	if err != nil {
		t.Fatal(err)
	}
	m, err := Parse(sig)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Verify(keys); err != nil {
		t.Fatal(err)
	}
	if m.Entries[0].Name != "cover.jpg" {
		t.Fatal("entries not sorted")
	}
	tampered := bytes.Replace(sig, []byte("track.mp3"), []byte("track.mp4"), 1)
	if m2, err := Parse(tampered); err != nil || m2.Verify(keys) == nil {
		t.Fatal("tampered manifest verified")
	}
	for _, bad := range []string{"", "spor-manifest v1\nmac 00\n", strings.Replace(string(sig), "alg ed25519", "alg hmac", 1)} {
		if m, err := Parse([]byte(bad)); err == nil && m.Verify(keys) == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	if _, err := Sign(priv, []Entry{{"../x", h}}); err == nil {
		t.Fatal("accepted path in name")
	}
}
