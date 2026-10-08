// Package server serves the verified library over HTTP.
package server

import (
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pynezz/spor/internal/library"
	"github.com/pynezz/spor/web"
)

const csp = "default-src 'self'; img-src 'self' data: blob:; media-src 'self' blob:; " +
	"style-src 'self'; script-src 'self'; connect-src 'self'; worker-src 'self'; " +
	"object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// Server is the HTTP front of spor.
type Server struct {
	cfg     Config
	log     *slog.Logger
	scanner *library.Scanner
	keys    map[string]ed25519.PublicKey
	keysTxt []byte

	snap     atomic.Pointer[snapshot]
	scanMu   sync.Mutex
	static   map[string]staticFile
	statHash string
	tmpl     *template.Template
	strs     map[string]map[string]string
	strsRaw  map[string]string
	limiter  *limiter
	playMu   sync.Mutex
	plays    *os.File
}

type snapshot struct {
	idx  *library.Index
	json []byte
	gz   []byte
	etag string
}

type staticFile struct {
	data  []byte
	gz    []byte
	ctype string
}

// New builds a server and runs the first scan.
func New(cfg Config, keys map[string]ed25519.PublicKey, log *slog.Logger) (*Server, error) {
	s := &Server{
		cfg:  cfg,
		log:  log,
		keys: keys,
		scanner: &library.Scanner{
			Library: cfg.Library,
			Cache:   cfg.Cache,
			Keys:    keys,
			Log:     log,
		},
		limiter: newLimiter(cfg.MediaRate, cfg.MediaBurst),
	}
	ids := make([]string, 0, len(keys))
	for id := range keys {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var kb bytes.Buffer
	for _, id := range ids {
		fmt.Fprintf(&kb, "ed25519 %s %s\n", base64.StdEncoding.EncodeToString(keys[id]), id)
	}
	s.keysTxt = kb.Bytes()
	if err := s.loadAssets(); err != nil {
		return nil, err
	}
	if cfg.PlayLog {
		if err := os.MkdirAll(cfg.Cache, 0o755); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(filepath.Join(cfg.Cache, "plays.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, err
		}
		s.plays = f
	}
	if err := s.Rescan(); err != nil {
		return nil, err
	}
	return s, nil
}

// Rescan rebuilds the index and swaps it in atomically.
func (s *Server) Rescan() error {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	start := time.Now()
	idx, err := s.scanner.Scan()
	if err != nil {
		return err
	}
	snap := &snapshot{idx: idx}
	var list []trackJSON
	for _, t := range idx.Tracks {
		if t.Visibility == library.Public {
			list = append(list, s.trackJSON(t, ""))
		}
	}
	if list == nil {
		list = []trackJSON{}
	}
	snap.json, err = json.Marshal(map[string]any{"tracks": list})
	if err != nil {
		return err
	}
	sum := sha256.Sum256(snap.json)
	snap.etag = `"` + hex.EncodeToString(sum[:12]) + `"`
	snap.gz = gzipBytes(snap.json)
	prev := s.snap.Swap(snap)
	if prev == nil || prev.etag != snap.etag || len(prev.idx.Tracks) != len(idx.Tracks) {
		s.log.Info("library indexed", "tracks", len(idx.Tracks), "took", time.Since(start).Round(time.Millisecond))
	}
	return nil
}

// Run rescans every interval until stop is closed.
func (s *Server) Run(stop <-chan struct{}, hup <-chan os.Signal) {
	var tick <-chan time.Time
	if s.cfg.RescanInterval > 0 {
		t := time.NewTicker(s.cfg.RescanInterval)
		defer t.Stop()
		tick = t.C
	}
	gc := time.NewTicker(5 * time.Minute)
	defer gc.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick:
		case <-hup:
			s.log.Info("rescan requested")
		case <-gc.C:
			s.limiter.gc()
			continue
		}
		if err := s.Rescan(); err != nil {
			s.log.Error("rescan failed", "err", err)
		}
	}
}

func gzipBytes(b []byte) []byte {
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	zw.Write(b)
	zw.Close()
	return buf.Bytes()
}

func (s *Server) loadAssets() error {
	s.static = map[string]staticFile{}
	h := sha256.New()
	err := fs.WalkDir(web.FS, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := web.FS.ReadFile(p)
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(p, "static/")
		h.Write([]byte(name))
		h.Write(b)
		sf := staticFile{data: b, ctype: ctypeFor(name)}
		if strings.HasPrefix(sf.ctype, "text/") || strings.Contains(sf.ctype, "javascript") || strings.Contains(sf.ctype, "svg") || strings.Contains(sf.ctype, "json") {
			sf.gz = gzipBytes(b)
		}
		s.static[name] = sf
		return nil
	})
	if err != nil {
		return err
	}
	sw, err := web.FS.ReadFile("sw.js")
	if err != nil {
		return err
	}
	h.Write(sw)
	s.statHash = hex.EncodeToString(h.Sum(nil))[:12]
	s.static["/sw.js"] = staticFile{data: sw, gz: gzipBytes(sw), ctype: "text/javascript; charset=utf-8"}

	s.strs = map[string]map[string]string{}
	s.strsRaw = map[string]string{}
	for _, lang := range []string{"nb", "en-US"} {
		b, err := web.FS.ReadFile("strings/" + lang + ".json")
		if err != nil {
			return err
		}
		m := map[string]string{}
		if err := json.Unmarshal(b, &m); err != nil {
			return fmt.Errorf("strings/%s.json: %w", lang, err)
		}
		s.strs[lang] = m
		compact, _ := json.Marshal(m)
		s.strsRaw[lang] = string(compact)
	}
	s.tmpl, err = template.New("").Funcs(template.FuncMap{
		"static": func(name string) string { return "/static/" + s.statHash + "/" + name },
	}).ParseFS(web.FS, "templates/*.html")
	return err
}

func ctypeFor(name string) string {
	switch path.Ext(name) {
	case ".js":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".json", ".webmanifest":
		return "application/manifest+json"
	case ".png":
		return "image/png"
	case ".woff2":
		return "font/woff2"
	}
	return "application/octet-stream"
}

// Handler returns the routed handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /t/{slug}", s.handleShare)
	mux.HandleFunc("GET /api/library.json", s.handleLibrary)
	mux.HandleFunc("GET /api/sig/{slug}", s.handleSig)
	mux.HandleFunc("GET /api/keys", s.handleKeys)
	mux.HandleFunc("POST /api/play/{slug}", s.handlePlay)
	mux.HandleFunc("GET /m/{file}", s.handleMedia)
	mux.HandleFunc("GET /c/{file}", s.handleCover)
	mux.HandleFunc("GET /p/{file}", s.handlePeaks)
	mux.HandleFunc("GET /static/{hash}/{name...}", s.handleStatic)
	mux.HandleFunc("GET /sw.js", s.handleSW)
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/static/"+s.statHash+"/icon.svg", http.StatusFound)
	})
	mux.HandleFunc("GET /robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("User-agent: *\nDisallow: /m/\nDisallow: /api/\n"))
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok\n"))
	})
	return s.secure(mux)
}

func (s *Server) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy", csp)
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), interest-cohort=()")
		next.ServeHTTP(w, r)
	})
}

// pickLang implements the Accept-Language rule from the plan: the preferred
// language decides; nb, no and nn map to nb, anything else to en-US. With no
// header at all we default to nb.
func pickLang(header string) string {
	best, bestQ := "", -1.0
	for _, part := range strings.Split(header, ",") {
		tag, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		tag = strings.ToLower(strings.TrimSpace(tag))
		if tag == "" {
			continue
		}
		q := 1.0
		if v, ok := strings.CutPrefix(strings.TrimSpace(params), "q="); ok {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				q = f
			}
		}
		if q > bestQ {
			best, bestQ = tag, q
		}
	}
	if best == "" || best == "*" {
		return "nb"
	}
	primary, _, _ := strings.Cut(best, "-")
	switch primary {
	case "nb", "no", "nn":
		return "nb"
	}
	return "en-US"
}

type langCtx struct {
	Lang      string // html lang
	OGLocale  string
	S         map[string]string
	StringsJS string
}

func (s *Server) lang(r *http.Request) langCtx {
	l := pickLang(r.Header.Get("Accept-Language"))
	lc := langCtx{Lang: l, S: s.strs[l], StringsJS: s.strsRaw[l]}
	if l == "nb" {
		lc.OGLocale = "nb_NO"
	} else {
		lc.OGLocale = "en_US"
	}
	return lc
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	lc := s.lang(r)
	data := map[string]any{
		"L":         lc,
		"PublicURL": s.cfg.PublicURL,
		"SW":        s.swURL(),
	}
	s.render(w, "index.html", data)
}

func (s *Server) swURL() string {
	return fmt.Sprintf("/sw.js?v=%s&l=%d", s.statHash, s.cfg.SWCacheLimitMB)
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		s.log.Error("template", "name", name, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	h.Add("Vary", "Accept-Language")
	w.Write(buf.Bytes())
}

// lookup finds a track by slug, enforcing the unlisted token.
func (s *Server) lookup(r *http.Request) (*library.Track, string) {
	snap := s.snap.Load()
	t := snap.idx.BySlug[r.PathValue("slug")]
	if t == nil {
		return nil, ""
	}
	if t.Visibility == library.Unlisted {
		k := r.URL.Query().Get("k")
		if subtle.ConstantTimeCompare([]byte(k), []byte(t.Token)) != 1 {
			return nil, ""
		}
		return t, k
	}
	return t, ""
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	lc := s.lang(r)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Add("Vary", "Accept-Language")
	w.WriteHeader(http.StatusNotFound)
	w.Write([]byte(lc.S["notFound"] + "\n"))
}

func (s *Server) handleShare(w http.ResponseWriter, r *http.Request) {
	t, token := s.lookup(r)
	if t == nil {
		s.notFound(w, r)
		return
	}
	lc := s.lang(r)
	tj := s.trackJSON(t, token)
	raw, _ := json.Marshal(tj)
	share := s.cfg.PublicURL + "/t/" + t.Slug
	if token != "" {
		share += "?k=" + token
		w.Header().Set("X-Robots-Tag", "noindex")
	}
	desc := t.Description
	if desc == "" {
		desc = t.Style
	}
	if desc == "" {
		desc = lc.S["tagline"]
	}
	var ogAudio *sourceJSON
	for _, a := range tj.Audio {
		if ogAudio == nil || a.Type == "audio/mpeg" {
			a := a
			ogAudio = &a
		}
	}
	data := map[string]any{
		"OGAudio":   ogAudio,
		"L":         lc,
		"T":         t,
		"J":         tj,
		"TrackJSON": string(raw),
		"ShareURL":  share,
		"Desc":      desc,
		"Abs":       s.cfg.PublicURL,
		"SW":        s.swURL(),
		"Unlisted":  token != "",
		"Duration":  fmtDuration(t.Duration),
	}
	s.render(w, "share.html", data)
}

func fmtDuration(d float64) string {
	if d <= 0 {
		return ""
	}
	n := int(d + 0.5)
	return fmt.Sprintf("%d:%02d", n/60, n%60)
}

type sourceJSON struct {
	URL  string `json:"url"`
	Type string `json:"type"`
	Size int64  `json:"size"`
}

type trackJSON struct {
	Slug        string       `json:"slug"`
	Title       string       `json:"title"`
	Date        string       `json:"date,omitempty"`
	Style       string       `json:"style,omitempty"`
	Description string       `json:"description,omitempty"`
	Duration    float64      `json:"duration,omitempty"`
	Width       int          `json:"width,omitempty"`
	Height      int          `json:"height,omitempty"`
	Audio       []sourceJSON `json:"audio"`
	Video       *sourceJSON  `json:"video,omitempty"`
	CoverSm     string       `json:"coverSm,omitempty"`
	CoverLg     string       `json:"coverLg,omitempty"`
	Peaks       string       `json:"peaks,omitempty"`
	Lyrics      string       `json:"lyrics,omitempty"`
	Key         string       `json:"key"`
	Share       string       `json:"share"`
	Sig         string       `json:"sig"`
}

func src(a *library.Asset) sourceJSON {
	return sourceJSON{URL: "/m/" + a.SHA256 + "." + a.Ext, Type: a.MIME, Size: a.Size}
}

func (s *Server) trackJSON(t *library.Track, token string) trackJSON {
	j := trackJSON{
		Slug: t.Slug, Title: t.Title, Date: t.Date, Style: t.Style,
		Description: t.Description, Duration: round1(t.Duration),
		Width: t.Width, Height: t.Height, Lyrics: t.Lyrics, Key: t.KeyID,
		Audio: []sourceJSON{},
		Share: "/t/" + t.Slug,
		Sig:   "/api/sig/" + t.Slug,
	}
	if token != "" {
		j.Share += "?k=" + token
		j.Sig += "?k=" + token
	}
	for _, a := range t.Audio {
		j.Audio = append(j.Audio, src(a))
	}
	if t.Video != nil {
		v := src(t.Video)
		j.Video = &v
	}
	if t.CoverKey != "" {
		j.CoverSm = fmt.Sprintf("/c/%s-%d.jpg", t.CoverKey, library.CoverSizes[0])
		j.CoverLg = fmt.Sprintf("/c/%s-%d.jpg", t.CoverKey, library.CoverSizes[len(library.CoverSizes)-1])
	}
	if t.PeaksKey != "" {
		j.Peaks = "/p/" + t.PeaksKey + ".bin"
	}
	return j
}

func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }

func acceptsGzip(r *http.Request) bool {
	for _, p := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		enc, params, _ := strings.Cut(strings.TrimSpace(p), ";")
		if strings.TrimSpace(enc) == "gzip" && strings.ReplaceAll(params, " ", "") != "q=0" {
			return true
		}
	}
	return false
}

func etagMatch(r *http.Request, etag string) bool {
	for _, t := range strings.Split(r.Header.Get("If-None-Match"), ",") {
		t = strings.TrimPrefix(strings.TrimSpace(t), "W/")
		if t == etag || t == "*" {
			return true
		}
	}
	return false
}

// writeMaybeGz writes precompressed or plain bytes. The ETag differs per
// encoding so caches never mix them up.
func writeMaybeGz(w http.ResponseWriter, r *http.Request, etag string, plain, gz []byte) {
	h := w.Header()
	h.Add("Vary", "Accept-Encoding")
	body := plain
	if gz != nil && acceptsGzip(r) {
		body = gz
		h.Set("Content-Encoding", "gzip")
		etag = strings.TrimSuffix(etag, `"`) + `-gz"`
	}
	h.Set("ETag", etag)
	if etagMatch(r, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.Write(body)
}

func (s *Server) handleLibrary(w http.ResponseWriter, r *http.Request) {
	snap := s.snap.Load()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	writeMaybeGz(w, r, snap.etag, snap.json, snap.gz)
}

func (s *Server) handleSig(w http.ResponseWriter, r *http.Request) {
	t, _ := s.lookup(r)
	if t == nil {
		s.notFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	sum := sha256.Sum256(t.Manifest)
	etag := `"` + hex.EncodeToString(sum[:12]) + `"`
	writeMaybeGz(w, r, etag, t.Manifest, nil)
}

func (s *Server) handleKeys(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write(s.keysTxt)
}

func (s *Server) handlePlay(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.allow(s.clientIP(r)) {
		http.Error(w, "slow down", http.StatusTooManyRequests)
		return
	}
	t, _ := s.lookup(r)
	if t == nil {
		s.notFound(w, r)
		return
	}
	if s.plays != nil {
		line := time.Now().UTC().Format(time.RFC3339) + " " + t.Slug + "\n"
		s.playMu.Lock()
		_, err := s.plays.WriteString(line)
		s.playMu.Unlock()
		if err != nil {
			s.log.Warn("play log", "err", err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	sha, ext, ok := strings.Cut(r.PathValue("file"), ".")
	snap := s.snap.Load()
	a := snap.idx.Media[sha]
	if !ok || a == nil || a.Ext != ext {
		http.NotFound(w, r)
		return
	}
	if !s.limiter.allow(s.clientIP(r)) {
		w.Header().Set("Retry-After", "2")
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}
	f, err := os.Open(a.Path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	// Refuse bytes that changed after verification; the next scan catches up.
	if err != nil || fi.Size() != a.Size || !fi.ModTime().Equal(a.ModTime) {
		s.log.Warn("media changed on disk since verification", "path", a.Path)
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", a.MIME)
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	h.Set("ETag", `"`+a.SHA256+`"`)
	h.Set("Cross-Origin-Resource-Policy", "cross-origin")
	http.ServeContent(w, r, "", a.ModTime, f)
}

func (s *Server) serveCached(w http.ResponseWriter, r *http.Request, p, ctype, etag string) {
	f, err := os.Open(p)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	h.Set("ETag", etag)
	h.Set("Cross-Origin-Resource-Policy", "cross-origin")
	http.ServeContent(w, r, "", fi.ModTime(), f)
}

func (s *Server) handleCover(w http.ResponseWriter, r *http.Request) {
	key, ok := strings.CutSuffix(r.PathValue("file"), ".jpg")
	p := s.snap.Load().idx.Covers[key]
	if !ok || p == "" {
		http.NotFound(w, r)
		return
	}
	s.serveCached(w, r, p, "image/jpeg", `"`+key+`"`)
}

func (s *Server) handlePeaks(w http.ResponseWriter, r *http.Request) {
	key, ok := strings.CutSuffix(r.PathValue("file"), ".bin")
	p := s.snap.Load().idx.Peaks[key]
	if !ok || p == "" {
		http.NotFound(w, r)
		return
	}
	s.serveCached(w, r, p, "application/octet-stream", `"p-`+key+`"`)
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	sf, ok := s.static[r.PathValue("name")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", sf.ctype)
	if r.PathValue("hash") == s.statHash {
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "no-cache")
	}
	writeMaybeGz(w, r, `"`+s.statHash+`"`, sf.data, sf.gz)
}

func (s *Server) handleSW(w http.ResponseWriter, r *http.Request) {
	sf := s.static["/sw.js"]
	h := w.Header()
	h.Set("Content-Type", sf.ctype)
	h.Set("Cache-Control", "no-cache")
	h.Set("Service-Worker-Allowed", "/")
	writeMaybeGz(w, r, `"sw-`+s.statHash+`"`, sf.data, sf.gz)
}

// Close releases resources.
func (s *Server) Close() {
	if s.plays != nil {
		s.plays.Close()
	}
}
