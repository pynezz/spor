// Package manifest implements the track.sig format: a plain-text list of
// SHA-256 file hashes signed with Ed25519.
//
//	spor-manifest v1
//	alg ed25519
//	key 3f9a1c2e
//	sha256 <hex>  track.mp3
//	mac <hex signature over every byte above this line>
package manifest

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const (
	FileName = "track.sig"
	header   = "spor-manifest v1"
	alg      = "ed25519"
)

// Entry is one hashed file.
type Entry struct {
	Name   string
	SHA256 string // lowercase hex
}

// Manifest is a parsed track.sig.
type Manifest struct {
	KeyID   string
	Entries []Entry
	Signed  []byte // the bytes covered by the signature
	Sig     []byte
}

// Lookup returns the hash for name.
func (m *Manifest) Lookup(name string) (string, bool) {
	for _, e := range m.Entries {
		if e.Name == name {
			return e.SHA256, true
		}
	}
	return "", false
}

// ValidName reports whether name may appear in a manifest: a plain file name
// without separators, control characters or a leading dot.
func ValidName(name string) bool {
	if name == "" || name[0] == '.' || len(name) > 255 {
		return false
	}
	for _, r := range name {
		if r == '/' || r == '\\' || r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// Body renders the signed portion of a manifest. Entries are sorted by name.
func Body(keyID string, entries []Entry) ([]byte, error) {
	es := append([]Entry(nil), entries...)
	sort.Slice(es, func(i, j int) bool { return es[i].Name < es[j].Name })
	var b bytes.Buffer
	b.WriteString(header + "\n")
	b.WriteString("alg " + alg + "\n")
	b.WriteString("key " + keyID + "\n")
	for i, e := range es {
		if !ValidName(e.Name) {
			return nil, fmt.Errorf("invalid file name %q", e.Name)
		}
		if i > 0 && es[i-1].Name == e.Name {
			return nil, fmt.Errorf("duplicate file %q", e.Name)
		}
		if len(e.SHA256) != 64 {
			return nil, fmt.Errorf("bad hash for %q", e.Name)
		}
		fmt.Fprintf(&b, "sha256 %s  %s\n", e.SHA256, e.Name)
	}
	return b.Bytes(), nil
}

// Sign produces a complete manifest.
func Sign(priv ed25519.PrivateKey, entries []Entry) ([]byte, error) {
	pub := priv.Public().(ed25519.PublicKey)
	body, err := Body(KeyID(pub), entries)
	if err != nil {
		return nil, err
	}
	sig := ed25519.Sign(priv, body)
	return append(body, []byte("mac "+hex.EncodeToString(sig)+"\n")...), nil
}

// Parse parses a manifest without checking the signature.
func Parse(data []byte) (*Manifest, error) {
	i := bytes.LastIndex(data, []byte("\nmac "))
	if i < 0 {
		return nil, errors.New("missing mac line")
	}
	signed := data[:i+1]
	macLine := strings.TrimRight(string(data[i+1:]), "\n")
	if strings.Contains(macLine, "\n") {
		return nil, errors.New("trailing data after mac line")
	}
	sig, err := hex.DecodeString(strings.TrimPrefix(macLine, "mac "))
	if err != nil || len(sig) != ed25519.SignatureSize {
		return nil, errors.New("malformed mac")
	}
	m := &Manifest{Signed: signed, Sig: sig}
	sc := bufio.NewScanner(bytes.NewReader(signed))
	n := 0
	seen := map[string]bool{}
	for sc.Scan() {
		line := sc.Text()
		n++
		switch {
		case n == 1:
			if line != header {
				return nil, fmt.Errorf("unsupported header %q", line)
			}
		case n == 2:
			if line != "alg "+alg {
				return nil, fmt.Errorf("unsupported algorithm line %q", line)
			}
		case n == 3:
			id, ok := strings.CutPrefix(line, "key ")
			if !ok || id == "" {
				return nil, errors.New("missing key line")
			}
			m.KeyID = id
		default:
			rest, ok := strings.CutPrefix(line, "sha256 ")
			if !ok {
				return nil, fmt.Errorf("line %d: unexpected %q", n, line)
			}
			h, name, ok := strings.Cut(rest, "  ")
			if !ok || len(h) != 64 || !ValidName(name) {
				return nil, fmt.Errorf("line %d: malformed entry", n)
			}
			if _, err := hex.DecodeString(h); err != nil || strings.ToLower(h) != h {
				return nil, fmt.Errorf("line %d: malformed hash", n)
			}
			if seen[name] {
				return nil, fmt.Errorf("duplicate entry %q", name)
			}
			seen[name] = true
			m.Entries = append(m.Entries, Entry{Name: name, SHA256: h})
		}
	}
	if n < 3 {
		return nil, errors.New("truncated manifest")
	}
	return m, sc.Err()
}

// Verify checks the signature against the matching key in keys.
func (m *Manifest) Verify(keys map[string]ed25519.PublicKey) error {
	pub, ok := keys[m.KeyID]
	if !ok {
		return fmt.Errorf("unknown key %s", m.KeyID)
	}
	if !ed25519.Verify(pub, m.Signed, m.Sig) {
		return errors.New("bad signature")
	}
	return nil
}

// KeyID is the first four bytes of SHA-256 over the raw public key, in hex.
func KeyID(pub ed25519.PublicKey) string {
	h := sha256.Sum256(pub)
	return hex.EncodeToString(h[:4])
}
