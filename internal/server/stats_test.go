package server

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStatsLikesOncePerSession(t *testing.T) {
	dir := t.TempDir()
	plays, likes := filepath.Join(dir, "plays.log"), filepath.Join(dir, "likes.log")
	st, err := openStats(plays, likes)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := sessionHash("0123456789abcdef0123456789abcdef")
	b, _ := sessionHash("fedcba9876543210fedcba9876543210")
	if n, err := st.like("x", a); n != 1 || err != nil {
		t.Fatalf("first like: %d %v", n, err)
	}
	if n, err := st.like("x", a); n != 1 || !errors.Is(err, errLiked) {
		t.Fatalf("repeat like: %d %v", n, err)
	}
	if n, err := st.like("x", b); n != 2 || err != nil {
		t.Fatalf("other session: %d %v", n, err)
	}
	st.play("x")
	st.play("x")
	st.close()

	// Counts survive a restart and the dedup still holds.
	st, err = openStats(plays, likes)
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	if g := st.get("x", a); g.Plays != 2 || g.Likes != 2 || !g.Liked {
		t.Fatalf("after reload: %+v", g)
	}
	if _, err := st.like("x", b); !errors.Is(err, errLiked) {
		t.Fatalf("reloaded repeat like: %v", err)
	}
	if g := st.get("x", ""); g.Liked {
		t.Fatal("anonymous get must not report liked")
	}
	raw, _ := os.ReadFile(likes)
	if len(raw) == 0 || string(raw) == "" {
		t.Fatal("likes.log empty")
	}
}

func TestSessionHash(t *testing.T) {
	for _, bad := range []string{"", "short", "zz23456789abcdef0123456789abcdef", "0123456789abcdef0123456789abcdef0"} {
		if _, ok := sessionHash(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}
