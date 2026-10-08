// Package library scans the track directories, verifies their signed
// manifests and produces an immutable Index of servable tracks.
package library

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path"
	"strings"
	"time"
)

// Visibility of a track.
const (
	Public   = "public"
	Unlisted = "unlisted"
	Draft    = "draft"
)

// Asset is one verified, servable file.
type Asset struct {
	SHA256  string
	Ext     string
	MIME    string
	Path    string
	Size    int64
	ModTime time.Time
}

// Track is a verified track directory.
type Track struct {
	Slug        string
	Title       string
	Date        string
	Style       string
	Description string
	Visibility  string
	Token       string

	Audio  []*Asset // preferred source first
	Video  *Asset
	Lyrics string

	CoverKey string // key for /c/<key>-<size>.jpg; empty if no cover
	CoverW   int    // dimensions of the large cover thumbnail
	CoverH   int

	Duration float64
	Width    int
	Height   int
	PeaksKey string // key for /p/<key>.bin; empty if no peaks

	KeyID    string
	Manifest []byte
	Updated  time.Time

	coverSrc string
	peaksSrc string
}

// Primary returns the main media asset: video if present, else first audio.
func (t *Track) Primary() *Asset {
	if t.Video != nil {
		return t.Video
	}
	if len(t.Audio) > 0 {
		return t.Audio[0]
	}
	return nil
}

// Index is an immutable snapshot of the library.
type Index struct {
	Tracks []*Track // all servable tracks (public and unlisted), newest first
	BySlug map[string]*Track
	Media  map[string]*Asset // sha256 -> asset
	Covers map[string]string // "<key>-<size>" -> cached jpeg path
	Peaks  map[string]string // key -> peaks file path
	Built  time.Time
}

// Thumbnail sizes for covers.
var CoverSizes = []int{320, 1200}

// Role of a file in a track directory.
type Role int

const (
	RoleNone Role = iota
	RoleAudio
	RoleVideo
	RoleCover
	RoleLyrics
	RoleMeta
	RolePeaks
)

var audioExt = map[string]string{
	"opus": "audio/ogg; codecs=opus",
	"ogg":  "audio/ogg",
	"oga":  "audio/ogg",
	"webm": "audio/webm",
	"m4a":  "audio/mp4",
	"aac":  "audio/aac",
	"flac": "audio/flac",
	"mp3":  "audio/mpeg",
}

// audioPref orders sources: smaller modern codecs first, mp3 as fallback.
var audioPref = []string{"opus", "webm", "ogg", "oga", "m4a", "aac", "flac", "mp3"}

var videoExt = map[string]string{"mp4": "video/mp4", "m4v": "video/mp4"}

// RoleOf classifies a file name.
func RoleOf(name string) (Role, string) {
	base, ext, _ := strings.Cut(name, ".")
	ext = strings.ToLower(ext)
	switch {
	case base == "track" && audioExt[ext] != "":
		return RoleAudio, ext
	case base == "track" && videoExt[ext] != "":
		return RoleVideo, ext
	case base == "cover" && (ext == "jpg" || ext == "jpeg" || ext == "png"):
		return RoleCover, ext
	case name == "lyrics.txt":
		return RoleLyrics, ext
	case name == "meta.toml" || name == "meta.json":
		return RoleMeta, ext
	case name == "peaks.bin":
		return RolePeaks, ext
	}
	return RoleNone, ext
}

// MIMEFor returns the content type for a media extension.
func MIMEFor(ext string) string {
	if m := audioExt[ext]; m != "" {
		return m
	}
	if m := videoExt[ext]; m != "" {
		return m
	}
	switch ext {
	case "jpg", "jpeg":
		return "image/jpeg"
	case "png":
		return "image/png"
	}
	return "application/octet-stream"
}

// ValidSlug reports whether a directory name can be a URL slug.
func ValidSlug(s string) bool {
	if s == "" || len(s) > 100 || s[0] == '.' || s[0] == '-' {
		return false
	}
	for _, r := range s {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.'
		if !ok {
			return false
		}
	}
	return path.Clean(s) == s
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// HashFile returns the hex SHA-256 of a file.
func HashFile(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
