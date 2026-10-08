// Command spor serves a signed, self-hosted music library and provides the
// workstation tooling to sign and publish tracks.
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/pynezz/spor/internal/conf"
	"github.com/pynezz/spor/internal/library"
	"github.com/pynezz/spor/internal/manifest"
	"github.com/pynezz/spor/internal/media"
	"github.com/pynezz/spor/internal/server"
)

var version = "dev"

const usage = `spor: self-hosted music library

Usage:
  spor serve   [-config spor.toml] [flags]   run the server
  spor keygen  [-out DIR]                    create an Ed25519 keypair
  spor sign    [-key FILE] DIR               sign one track directory
  spor sign    [-key FILE] -all LIBRARY      sign every unsigned or changed track
  spor verify  [-pub FILE] DIR|LIBRARY       dry run: report problems
  spor publish [-key FILE] [-remote HOST:PATH] DIR
                                             sign, rsync to the server, reload
  spor token                                 print a fresh unlisted token
  spor version

Environment:
  SPOR_KEY      private key path   (default ~/.config/spor/spor.key)
  SPOR_PUB      public key path    (default ~/.config/spor/spor.pub)
  SPOR_REMOTE   rsync target for publish, e.g. vps:/srv/spor/library
  SPOR_RELOAD   remote command run over ssh after publish
                (default: systemctl kill -s HUP spor.service)
  SPOR_URL      public base URL    (default https://lytt.txtv.no)
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	args := os.Args[2:]
	switch os.Args[1] {
	case "serve":
		err = cmdServe(args)
	case "keygen":
		err = cmdKeygen(args)
	case "sign":
		err = cmdSign(args)
	case "verify":
		err = cmdVerify(args)
	case "publish":
		err = cmdPublish(args)
	case "token":
		fmt.Println(newToken())
	case "version", "-v", "--version":
		fmt.Println("spor", version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "spor:", err)
		os.Exit(1)
	}
}

func configDir() string {
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "spor")
	}
	return "."
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func newToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

/* ---------- serve ---------- */

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	cfgPath := fs.String("config", "", "config file (flat TOML); default /etc/spor/spor.toml if present")
	listen := fs.String("listen", "", "listen address")
	lib := fs.String("library", "", "library directory")
	cache := fs.String("cache", "", "cache directory (writable)")
	pub := fs.String("pub", "", "public key file(s), comma separated")
	pubURL := fs.String("url", "", "public base URL")
	debug := fs.Bool("debug", false, "debug logging")
	fs.Parse(args)

	cfg := server.DefaultConfig()
	path := *cfgPath
	if path == "" {
		if _, err := os.Stat("/etc/spor/spor.toml"); err == nil {
			path = "/etc/spor/spor.toml"
		}
	}
	if path != "" {
		var err error
		if cfg, err = server.LoadConfig(path); err != nil {
			return err
		}
	}
	if *listen != "" {
		cfg.Listen = *listen
	}
	if *lib != "" {
		cfg.Library = *lib
	}
	if *cache != "" {
		cfg.Cache = *cache
	}
	if *pub != "" {
		cfg.PublicKeys = strings.Split(*pub, ",")
	}
	if *pubURL != "" {
		cfg.PublicURL = *pubURL
	}
	if err := cfg.Finish(); err != nil {
		return err
	}
	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	keys, err := manifest.LoadPublicKeys(cfg.PublicKeys...)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(keys))
	for id := range keys {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	log.Info("starting", "version", version, "listen", cfg.Listen, "library", cfg.Library, "keys", strings.Join(ids, ","), "ffmpeg", media.HaveFFmpeg())

	srv, err := server.New(cfg, keys, log)
	if err != nil {
		return err
	}
	defer srv.Close()

	hs := &http.Server{
		Addr:              cfg.Listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}
	stop := make(chan struct{})
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	go srv.Run(stop, hup)

	errc := make(chan error, 1)
	go func() { errc <- hs.ListenAndServe() }()
	term := make(chan os.Signal, 1)
	signal.Notify(term, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-errc:
		return err
	case s := <-term:
		log.Info("shutting down", "signal", s.String())
	}
	close(stop)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return hs.Shutdown(ctx)
}

/* ---------- keygen ---------- */

func cmdKeygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	out := fs.String("out", configDir(), "directory for spor.key and spor.pub")
	comment := fs.String("comment", "", "comment stored with the public key")
	fs.Parse(args)
	if err := os.MkdirAll(*out, 0o700); err != nil {
		return err
	}
	privPath, pubPath := filepath.Join(*out, "spor.key"), filepath.Join(*out, "spor.pub")
	for _, p := range []string{privPath, pubPath} {
		if _, err := os.Stat(p); err == nil {
			return fmt.Errorf("%s exists; refusing to overwrite", p)
		}
	}
	pub, priv, id, err := manifest.GenerateKey(*comment)
	if err != nil {
		return err
	}
	if err := os.WriteFile(privPath, priv, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(pubPath, pub, 0o644); err != nil {
		return err
	}
	fmt.Printf("key %s\n  private: %s (keep on this machine)\n  public:  %s (copy to the server)\n", id, privPath, pubPath)
	return nil
}

/* ---------- sign ---------- */

type signOpts struct {
	keyPath string
	all     bool
	force   bool
	ingest  bool
	baseURL string
}

func signFlags(name string, args []string) (signOpts, []string) {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	var o signOpts
	fs.StringVar(&o.keyPath, "key", envOr("SPOR_KEY", filepath.Join(configDir(), "spor.key")), "private key file")
	if name == "sign" {
		fs.BoolVar(&o.all, "all", false, "treat the argument as a library and sign every changed track")
	}
	fs.BoolVar(&o.force, "force", false, "re-sign even if nothing changed")
	fs.BoolVar(&o.ingest, "ingest", true, "generate peaks.bin and a video cover with ffmpeg when missing")
	fs.StringVar(&o.baseURL, "url", envOr("SPOR_URL", "https://lytt.txtv.no"), "public base URL for printed links")
	fs.Parse(args)
	return o, fs.Args()
}

func cmdSign(args []string) error {
	o, rest := signFlags("sign", args)
	if len(rest) != 1 {
		return errors.New("usage: spor sign [-key FILE] [-all] DIR")
	}
	priv, err := manifest.LoadPrivateKey(o.keyPath)
	if err != nil {
		return err
	}
	if !o.all {
		_, err := signDir(rest[0], priv, o)
		return err
	}
	ents, err := os.ReadDir(rest[0])
	if err != nil {
		return err
	}
	var failed int
	for _, e := range ents {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if _, err := signDir(filepath.Join(rest[0], e.Name()), priv, o); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", e.Name(), err)
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d track(s) failed", failed)
	}
	return nil
}

// signDir prepares and signs one track directory. It returns the share URL.
func signDir(dir string, key ed25519.PrivateKey, o signOpts) (string, error) {
	dir = filepath.Clean(dir)
	slug := filepath.Base(dir)
	if !library.ValidSlug(slug) {
		return "", fmt.Errorf("%q is not a valid slug (use a-z, 0-9, '-', '_', '.')", slug)
	}
	var old *manifest.Manifest
	if raw, err := os.ReadFile(filepath.Join(dir, manifest.FileName)); err == nil {
		old, _ = manifest.Parse(raw)
	}

	token, err := ensureToken(dir)
	if err != nil {
		return "", err
	}

	names, err := trackFiles(dir)
	if err != nil {
		return "", err
	}
	var primary string
	var hasCover, hasPeaks, hasVideo bool
	for _, n := range names {
		role, ext := library.RoleOf(n)
		switch role {
		case library.RoleVideo:
			primary, hasVideo = n, true
		case library.RoleAudio:
			if primary == "" || ext == "mp3" && !hasVideo {
				primary = n
			}
		case library.RoleCover:
			hasCover = true
		case library.RolePeaks:
			hasPeaks = true
		}
		if strings.HasSuffix(strings.ToLower(n), ".wav") {
			fmt.Fprintf(os.Stderr, "%s: %s is WAV; it will not be served. Transcode to Opus/MP3 first.\n", slug, n)
		}
	}
	if primary == "" {
		return "", errors.New("no track.<mp3|m4a|opus|ogg|webm|flac|mp4> found")
	}
	primaryPath := filepath.Join(dir, primary)
	primarySHA, err := library.HashFile(primaryPath)
	if err != nil {
		return "", err
	}

	if o.ingest && media.HaveFFmpeg() {
		stale := false
		if old != nil {
			if h, ok := old.Lookup(primary); ok && h != primarySHA {
				stale = true
			}
		}
		if !hasPeaks || stale {
			if peaks, err := media.Peaks(primaryPath); err != nil {
				fmt.Fprintf(os.Stderr, "%s: peaks: %v\n", slug, err)
			} else if err := os.WriteFile(filepath.Join(dir, "peaks.bin"), peaks, 0o644); err != nil {
				return "", err
			}
		}
		if !hasCover && hasVideo {
			if frame, err := media.VideoFrame(primaryPath); err != nil {
				fmt.Fprintf(os.Stderr, "%s: cover from video: %v\n", slug, err)
			} else if err := os.WriteFile(filepath.Join(dir, "cover.jpg"), frame, 0o644); err != nil {
				return "", err
			}
		}
		if names, err = trackFiles(dir); err != nil {
			return "", err
		}
	}

	var entries []manifest.Entry
	for _, n := range names {
		sum := primarySHA
		if n != primary {
			if sum, err = library.HashFile(filepath.Join(dir, n)); err != nil {
				return "", err
			}
		}
		entries = append(entries, manifest.Entry{Name: n, SHA256: sum})
	}
	url := o.baseURL + "/t/" + slug
	if token != "" {
		url += "?k=" + token
	}
	if !o.force && old != nil && old.KeyID == manifest.KeyID(key.Public().(ed25519.PublicKey)) && sameEntries(old.Entries, entries) {
		if old.Verify(map[string]ed25519.PublicKey{old.KeyID: key.Public().(ed25519.PublicKey)}) == nil {
			fmt.Printf("%s: unchanged  %s\n", slug, url)
			return url, nil
		}
	}
	sig, err := manifest.Sign(key, entries)
	if err != nil {
		return "", err
	}
	if err := media.WriteFileAtomic(filepath.Join(dir, manifest.FileName), sig, 0o644); err != nil {
		return "", err
	}
	fmt.Printf("%s: signed %d files  %s\n", slug, len(entries), url)
	return url, nil
}

// trackFiles lists the regular, non-hidden files to include in a manifest.
func trackFiles(dir string) ([]string, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range ents {
		n := e.Name()
		if n == manifest.FileName || strings.HasPrefix(n, ".") || strings.HasSuffix(n, ".tmp") || !e.Type().IsRegular() {
			continue
		}
		if !manifest.ValidName(n) {
			return nil, fmt.Errorf("unsupported file name %q", n)
		}
		names = append(names, n)
	}
	sort.Strings(names)
	return names, nil
}

func sameEntries(a, b []manifest.Entry) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]string{}
	for _, e := range a {
		m[e.Name] = e.SHA256
	}
	for _, e := range b {
		if m[e.Name] != e.SHA256 {
			return false
		}
	}
	return true
}

// ensureToken adds a token to meta.toml for unlisted tracks that lack one,
// and returns the token for unlisted tracks ("" otherwise).
func ensureToken(dir string) (string, error) {
	p := filepath.Join(dir, "meta.toml")
	raw, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	v, err := conf.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("meta.toml: %w", err)
	}
	vis, _ := v.String("visibility", library.Public)
	if strings.ToLower(vis) != library.Unlisted {
		return "", nil
	}
	tok, _ := v.String("token", "")
	if len(tok) >= 16 {
		return tok, nil
	}
	if tok != "" {
		return "", errors.New("meta.toml: token is shorter than 16 characters; remove it to generate one")
	}
	tok = newToken()
	if len(raw) > 0 && raw[len(raw)-1] != '\n' {
		raw = append(raw, '\n')
	}
	raw = append(raw, []byte("token = "+conf.Quote(tok)+"\n")...)
	if err := media.WriteFileAtomic(p, raw, 0o644); err != nil {
		return "", err
	}
	return tok, nil
}

/* ---------- verify ---------- */

func cmdVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	pubPath := fs.String("pub", envOr("SPOR_PUB", filepath.Join(configDir(), "spor.pub")), "public key file(s), comma separated")
	fs.Parse(args)
	if fs.NArg() != 1 {
		return errors.New("usage: spor verify [-pub FILE] DIR|LIBRARY")
	}
	keys, err := manifest.LoadPublicKeys(strings.Split(*pubPath, ",")...)
	if err != nil {
		return err
	}
	target := filepath.Clean(fs.Arg(0))
	var dirs []string
	if _, err := os.Stat(filepath.Join(target, manifest.FileName)); err == nil {
		dirs = []string{target}
	} else {
		ents, err := os.ReadDir(target)
		if err != nil {
			return err
		}
		for _, e := range ents {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
				dirs = append(dirs, filepath.Join(target, e.Name()))
			}
		}
	}
	bad := 0
	for _, d := range dirs {
		problems := verifyDir(d, keys)
		slug := filepath.Base(d)
		if len(problems) == 0 {
			fmt.Printf("ok    %s\n", slug)
			continue
		}
		bad++
		fmt.Printf("FAIL  %s\n", slug)
		for _, p := range problems {
			fmt.Printf("        %s\n", p)
		}
	}
	if bad > 0 {
		return fmt.Errorf("%d of %d track(s) have problems", bad, len(dirs))
	}
	return nil
}

func verifyDir(dir string, keys map[string]ed25519.PublicKey) []string {
	var probs []string
	if !library.ValidSlug(filepath.Base(dir)) {
		probs = append(probs, "directory name is not a valid slug")
	}
	raw, err := os.ReadFile(filepath.Join(dir, manifest.FileName))
	if err != nil {
		return append(probs, "no track.sig (run spor sign)")
	}
	m, err := manifest.Parse(raw)
	if err != nil {
		return append(probs, "track.sig: "+err.Error())
	}
	if err := m.Verify(keys); err != nil {
		probs = append(probs, "track.sig: "+err.Error())
	}
	listed := map[string]bool{}
	hasMedia := false
	for _, e := range m.Entries {
		listed[e.Name] = true
		role, _ := library.RoleOf(e.Name)
		if role == library.RoleAudio || role == library.RoleVideo {
			hasMedia = true
		}
		sum, err := library.HashFile(filepath.Join(dir, e.Name))
		switch {
		case err != nil:
			probs = append(probs, e.Name+": "+err.Error())
		case sum != e.SHA256:
			probs = append(probs, e.Name+": hash mismatch (changed after signing)")
		}
	}
	if !hasMedia {
		probs = append(probs, "no audio or video file in manifest")
	}
	if names, err := trackFiles(dir); err == nil {
		for _, n := range names {
			if !listed[n] {
				probs = append(probs, n+": not in manifest (ignored by the server)")
			}
		}
	}
	return probs
}

/* ---------- publish ---------- */

func cmdPublish(args []string) error {
	fs := flag.NewFlagSet("publish", flag.ExitOnError)
	keyPath := fs.String("key", envOr("SPOR_KEY", filepath.Join(configDir(), "spor.key")), "private key file")
	remote := fs.String("remote", os.Getenv("SPOR_REMOTE"), "rsync target library, e.g. vps:/srv/spor/library")
	reload := fs.String("reload", envOr("SPOR_RELOAD", "systemctl kill -s HUP spor.service"), "command run on the remote host after upload; empty to skip")
	baseURL := fs.String("url", envOr("SPOR_URL", "https://lytt.txtv.no"), "public base URL")
	fs.Parse(args)
	if fs.NArg() != 1 || *remote == "" {
		return errors.New("usage: spor publish -remote HOST:/srv/spor/library DIR (or set SPOR_REMOTE)")
	}
	priv, err := manifest.LoadPrivateKey(*keyPath)
	if err != nil {
		return err
	}
	dir := filepath.Clean(fs.Arg(0))
	url, err := signDir(dir, priv, signOpts{ingest: true, baseURL: strings.TrimRight(*baseURL, "/")})
	if err != nil {
		return err
	}
	target := strings.TrimRight(*remote, "/") + "/" + filepath.Base(dir) + "/"
	rs := exec.Command("rsync", "-rtz", "--delete", "--chmod=D755,F644", "--exclude=.*", "--exclude=*.tmp", dir+"/", target)
	rs.Stdout, rs.Stderr = os.Stdout, os.Stderr
	fmt.Println("rsync", dir+"/", "->", target)
	if err := rs.Run(); err != nil {
		return fmt.Errorf("rsync: %w", err)
	}
	if *reload != "" {
		host, _, ok := strings.Cut(*remote, ":")
		if ok && host != "" {
			ssh := exec.Command("ssh", host, *reload)
			ssh.Stdout, ssh.Stderr = os.Stdout, os.Stderr
			if err := ssh.Run(); err != nil {
				fmt.Fprintf(os.Stderr, "reload failed (%v); the server picks the track up on its next poll\n", err)
			}
		}
	}
	fmt.Println(url)
	return nil
}
