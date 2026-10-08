// Package conf parses the flat subset of TOML that spor uses for its config
// file and for per-track meta.toml: `key = value` pairs, where value is a
// basic string, a literal string, an integer, or a boolean. Tables, arrays and
// multi-line strings are rejected. This keeps the server free of dependencies.
package conf

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Values maps keys to their decoded values (string, int64 or bool).
type Values map[string]any

// ParseFile reads and parses a flat TOML file.
func ParseFile(path string) (Values, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	v, err := Parse(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return v, nil
}

// Parse parses flat TOML.
func Parse(b []byte) (Values, error) {
	out := Values{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		if line[0] == '[' {
			return nil, fmt.Errorf("line %d: tables are not supported", n)
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			return nil, fmt.Errorf("line %d: expected key = value", n)
		}
		key := strings.TrimSpace(line[:eq])
		if len(key) >= 2 && key[0] == '"' && key[len(key)-1] == '"' {
			key = key[1 : len(key)-1]
		}
		if key == "" {
			return nil, fmt.Errorf("line %d: empty key", n)
		}
		val, err := parseValue(strings.TrimSpace(line[eq+1:]))
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		if _, dup := out[key]; dup {
			return nil, fmt.Errorf("line %d: duplicate key %q", n, key)
		}
		out[key] = val
	}
	return out, sc.Err()
}

func parseValue(s string) (any, error) {
	if s == "" {
		return nil, fmt.Errorf("missing value")
	}
	switch s[0] {
	case '"':
		if strings.HasPrefix(s, `"""`) {
			return nil, fmt.Errorf("multi-line strings are not supported")
		}
		var sb strings.Builder
		for i := 1; i < len(s); i++ {
			c := s[i]
			switch c {
			case '"':
				return sb.String(), trailing(s[i+1:])
			case '\\':
				i++
				if i >= len(s) {
					return nil, fmt.Errorf("unterminated escape")
				}
				switch s[i] {
				case 'n':
					sb.WriteByte('\n')
				case 't':
					sb.WriteByte('\t')
				case 'r':
					sb.WriteByte('\r')
				case '"', '\\':
					sb.WriteByte(s[i])
				case 'u', 'U':
					w := 4
					if s[i] == 'U' {
						w = 8
					}
					if i+w >= len(s) {
						return nil, fmt.Errorf("short unicode escape")
					}
					r, err := strconv.ParseUint(s[i+1:i+1+w], 16, 32)
					if err != nil {
						return nil, fmt.Errorf("bad unicode escape")
					}
					sb.WriteRune(rune(r))
					i += w
				default:
					return nil, fmt.Errorf("unknown escape \\%c", s[i])
				}
			default:
				sb.WriteByte(c)
			}
		}
		return nil, fmt.Errorf("unterminated string")
	case '\'':
		end := strings.IndexByte(s[1:], '\'')
		if end < 0 {
			return nil, fmt.Errorf("unterminated string")
		}
		return s[1 : 1+end], trailing(s[end+2:])
	}
	tok := s
	if i := strings.IndexByte(tok, '#'); i >= 0 {
		tok = strings.TrimSpace(tok[:i])
	}
	switch tok {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	i, err := strconv.ParseInt(strings.ReplaceAll(tok, "_", ""), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("unsupported value %q", tok)
	}
	return i, nil
}

func trailing(rest string) error {
	rest = strings.TrimSpace(rest)
	if rest == "" || rest[0] == '#' {
		return nil
	}
	return fmt.Errorf("unexpected text after value: %q", rest)
}

// String returns the string at key, or def when absent.
func (v Values) String(key, def string) (string, error) {
	x, ok := v[key]
	if !ok {
		return def, nil
	}
	s, ok := x.(string)
	if !ok {
		return "", fmt.Errorf("%s: expected string", key)
	}
	return s, nil
}

// Int returns the integer at key, or def when absent.
func (v Values) Int(key string, def int64) (int64, error) {
	x, ok := v[key]
	if !ok {
		return def, nil
	}
	i, ok := x.(int64)
	if !ok {
		return 0, fmt.Errorf("%s: expected integer", key)
	}
	return i, nil
}

// Bool returns the boolean at key, or def when absent.
func (v Values) Bool(key string, def bool) (bool, error) {
	x, ok := v[key]
	if !ok {
		return def, nil
	}
	b, ok := x.(bool)
	if !ok {
		return false, fmt.Errorf("%s: expected boolean", key)
	}
	return b, nil
}

// Quote encodes s as a TOML basic string.
func Quote(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			sb.WriteString(`\"`)
		case '\\':
			sb.WriteString(`\\`)
		case '\n':
			sb.WriteString(`\n`)
		case '\t':
			sb.WriteString(`\t`)
		case '\r':
			sb.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&sb, `\u%04X`, r)
			} else {
				sb.WriteRune(r)
			}
		}
	}
	sb.WriteByte('"')
	return sb.String()
}
