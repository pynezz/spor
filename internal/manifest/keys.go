package manifest

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Key file formats, one key per line, '#' starts a comment:
//
//	spor.pub:  ed25519 <base64 public key> [comment]
//	spor.key:  ed25519-private <base64 32-byte seed>

// GenerateKey creates a new keypair and returns both files' contents.
func GenerateKey(comment string) (pubFile, privFile []byte, id string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, "", err
	}
	id = KeyID(pub)
	line := "ed25519 " + base64.StdEncoding.EncodeToString(pub)
	if comment != "" {
		line += " " + comment
	}
	pubFile = []byte(fmt.Sprintf("# spor public key %s\n%s\n", id, line))
	privFile = []byte(fmt.Sprintf("# spor private key %s. Keep this on the workstation.\ned25519-private %s\n",
		id, base64.StdEncoding.EncodeToString(priv.Seed())))
	return pubFile, privFile, id, nil
}

// ParsePublicKeys parses a public key file holding one or more keys.
func ParsePublicKeys(data []byte) (map[string]ed25519.PublicKey, error) {
	out := map[string]ed25519.PublicKey{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 || strings.HasPrefix(f[0], "#") {
			continue
		}
		if f[0] != "ed25519" || len(f) < 2 {
			return nil, fmt.Errorf("unsupported key line %q", sc.Text())
		}
		raw, err := base64.StdEncoding.DecodeString(f[1])
		if err != nil || len(raw) != ed25519.PublicKeySize {
			return nil, errors.New("malformed public key")
		}
		pub := ed25519.PublicKey(raw)
		out[KeyID(pub)] = pub
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("no public keys found")
	}
	return out, nil
}

// LoadPublicKeys reads public keys from one or more files.
func LoadPublicKeys(paths ...string) (map[string]ed25519.PublicKey, error) {
	out := map[string]ed25519.PublicKey{}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		ks, err := ParsePublicKeys(b)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		for id, k := range ks {
			out[id] = k
		}
	}
	return out, nil
}

// LoadPrivateKey reads a private key file.
func LoadPrivateKey(path string) (ed25519.PrivateKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 || strings.HasPrefix(f[0], "#") {
			continue
		}
		if f[0] != "ed25519-private" || len(f) != 2 {
			return nil, fmt.Errorf("%s: unsupported key line", path)
		}
		seed, err := base64.StdEncoding.DecodeString(f[1])
		if err != nil || len(seed) != ed25519.SeedSize {
			return nil, fmt.Errorf("%s: malformed private key", path)
		}
		return ed25519.NewKeyFromSeed(seed), nil
	}
	return nil, fmt.Errorf("%s: no private key found", path)
}
