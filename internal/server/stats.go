package server

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"sync"
	"time"
)

// stats keeps play and like counts per slug. Plays come from plays.log
// ("<time> <slug>"), likes from likes.log ("<time> <slug> <session hash>").
// Both are append-only and replayed on start. A like is one per browser
// session per track; the session id is a random value the browser keeps in
// localStorage, so this stops double clicks, not determined spoofing.
type stats struct {
	mu    sync.Mutex
	plays map[string]int
	likes map[string]map[string]struct{}
	playF *os.File // nil: plays are counted but not persisted
	likeF *os.File // nil: likes are disabled
}

var errLiked = errors.New("already liked")

func openStats(playPath, likePath string) (*stats, error) {
	st := &stats{plays: map[string]int{}, likes: map[string]map[string]struct{}{}}
	if playPath != "" {
		if err := replay(playPath, func(f []string) {
			if len(f) >= 2 {
				st.plays[f[1]]++
			}
		}); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(playPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, err
		}
		st.playF = f
	}
	if likePath != "" {
		if err := replay(likePath, func(f []string) {
			if len(f) >= 3 {
				st.addLike(f[1], f[2])
			}
		}); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(likePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, err
		}
		st.likeF = f
	}
	return st, nil
}

func replay(path string, fn func([]string)) error {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fn(strings.Fields(sc.Text()))
	}
	return sc.Err()
}

func (st *stats) addLike(slug, sess string) bool {
	m := st.likes[slug]
	if m == nil {
		m = map[string]struct{}{}
		st.likes[slug] = m
	}
	if _, ok := m[sess]; ok {
		return false
	}
	m[sess] = struct{}{}
	return true
}

// sessionHash validates a client session id (32 hex characters) and returns
// the value that is stored, so the log does not hold the id itself.
func sessionHash(id string) (string, bool) {
	if len(id) != 32 {
		return "", false
	}
	if _, err := hex.DecodeString(id); err != nil {
		return "", false
	}
	sum := sha256.Sum256([]byte("spor-like\x00" + strings.ToLower(id)))
	return hex.EncodeToString(sum[:12]), true
}

func (st *stats) play(slug string) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.plays[slug]++
	if st.playF == nil {
		return nil
	}
	_, err := st.playF.WriteString(time.Now().UTC().Format(time.RFC3339) + " " + slug + "\n")
	return err
}

// like records one like and returns the new count, or errLiked with the
// current count if this session already liked the track.
func (st *stats) like(slug, sess string) (int, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.addLike(slug, sess) {
		return len(st.likes[slug]), errLiked
	}
	if _, err := st.likeF.WriteString(time.Now().UTC().Format(time.RFC3339) + " " + slug + " " + sess + "\n"); err != nil {
		delete(st.likes[slug], sess)
		return len(st.likes[slug]), err
	}
	return len(st.likes[slug]), nil
}

type trackStats struct {
	Plays int  `json:"plays"`
	Likes int  `json:"likes"`
	Liked bool `json:"liked,omitempty"`
}

func (st *stats) get(slug, sess string) trackStats {
	st.mu.Lock()
	defer st.mu.Unlock()
	_, liked := st.likes[slug][sess]
	return trackStats{Plays: st.plays[slug], Likes: len(st.likes[slug]), Liked: liked && sess != ""}
}

func (st *stats) close() {
	if st.playF != nil {
		st.playF.Close()
	}
	if st.likeF != nil {
		st.likeF.Close()
	}
}

// plural picks strings key+"1" or key+"N" for n.
func plural(strs map[string]string, key string, n int) string {
	if n == 1 {
		return strs[key+"1"]
	}
	return strs[key+"N"]
}
