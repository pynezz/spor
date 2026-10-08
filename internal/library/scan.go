package library

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/pynezz/spor/internal/conf"
	"github.com/pynezz/spor/internal/manifest"
	"github.com/pynezz/spor/internal/media"
)

type fileKey struct {
	Dev     uint64 `json:"dev"`
	Ino     uint64 `json:"ino"`
	Size    int64  `json:"size"`
	MtimeNs int64  `json:"mtime_ns"`
}

type fileState struct {
	fileKey
	SHA256 string `json:"sha256"`
}

type coverState struct {
	W   int    `json:"w"`
	H   int    `json:"h"`
	Err string `json:"err,omitempty"`
}

type state struct {
	Version int                   `json:"version"`
	Files   map[string]fileState  `json:"files"`
	Media   map[string]media.Info `json:"media"`
	Covers  map[string]coverState `json:"covers"`
	Peaks   map[string]string     `json:"peaks"` // sha -> "" ok, or error text
}

// Scanner produces Index snapshots. It is not safe for concurrent Scan calls.
type Scanner struct {
	Library string
	Cache   string
	Keys    map[string]ed25519.PublicKey
	Log     *slog.Logger

	st     *state
	warned map[string]bool
}

const stateVersion = 1

func (s *Scanner) statePath() string { return filepath.Join(s.Cache, "state.json") }

func (s *Scanner) loadState() {
	s.st = &state{}
	if b, err := os.ReadFile(s.statePath()); err == nil {
		if json.Unmarshal(b, s.st) != nil || s.st.Version != stateVersion {
			s.st = &state{}
		}
	}
	s.st.Version = stateVersion
	if s.st.Files == nil {
		s.st.Files = map[string]fileState{}
	}
	if s.st.Media == nil {
		s.st.Media = map[string]media.Info{}
	}
	if s.st.Covers == nil {
		s.st.Covers = map[string]coverState{}
	}
	if s.st.Peaks == nil {
		s.st.Peaks = map[string]string{}
	}
}

func (s *Scanner) saveState() error {
	b, err := json.Marshal(s.st)
	if err != nil {
		return err
	}
	return media.WriteFileAtomic(s.statePath(), b, 0o644)
}

func (s *Scanner) warnOnce(key, msg string, args ...any) {
	if s.warned[key] {
		return
	}
	s.warned[key] = true
	s.Log.Warn(msg, args...)
}

type candidate struct {
	slug, dir string
	m         *manifest.Manifest
	raw       []byte
	files     map[string]fileKey // listed name -> stat
	updated   time.Time
	err       error
}

// Scan walks the library and returns a fresh index.
func (s *Scanner) Scan() (*Index, error) {
	start := time.Now()
	if s.st == nil {
		s.loadState()
	}
	if s.warned == nil {
		s.warned = map[string]bool{}
	}
	if err := os.MkdirAll(s.Cache, 0o755); err != nil {
		return nil, err
	}
	ents, err := os.ReadDir(s.Library)
	if err != nil {
		return nil, err
	}
	var cands []*candidate
	for _, e := range ents {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		dir := filepath.Join(s.Library, name)
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			continue
		}
		if !ValidSlug(name) {
			s.warnOnce("slug:"+name, "skipping directory with invalid slug", "dir", name)
			continue
		}
		c := s.prepare(name, dir)
		if c.err != nil {
			s.warnOnce("reject:"+name+":"+c.err.Error(), "track rejected", "slug", name, "err", c.err)
			continue
		}
		cands = append(cands, c)
	}

	// Hash every listed file whose stat key changed, in parallel.
	type job struct {
		path string
		key  fileKey
	}
	var jobs []job
	for _, c := range cands {
		for name, k := range c.files {
			p := filepath.Join(c.dir, name)
			if fs, ok := s.st.Files[p]; !ok || fs.fileKey != k {
				jobs = append(jobs, job{p, k})
			}
		}
	}
	if len(jobs) > 0 {
		var mu sync.Mutex
		hashed := 0
		parallel(len(jobs), func(i int) {
			sum, err := HashFile(jobs[i].path)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				delete(s.st.Files, jobs[i].path)
				return
			}
			s.st.Files[jobs[i].path] = fileState{jobs[i].key, sum}
			hashed++
		})
		s.Log.Info("hashed files", "count", hashed, "took", time.Since(start).Round(time.Millisecond))
	}

	idx := &Index{
		BySlug: map[string]*Track{},
		Media:  map[string]*Asset{},
		Covers: map[string]string{},
		Peaks:  map[string]string{},
		Built:  time.Now(),
	}
	live := map[string]bool{}
	var tracks []*Track
	for _, c := range cands {
		for name := range c.files {
			live[filepath.Join(c.dir, name)] = true
		}
		t, err := s.assemble(c)
		if err != nil {
			s.warnOnce("reject:"+c.slug+":"+err.Error(), "track rejected", "slug", c.slug, "err", err)
			continue
		}
		if t != nil {
			tracks = append(tracks, t)
		}
	}
	for p := range s.st.Files {
		if !live[p] {
			delete(s.st.Files, p)
		}
	}

	// Ingest artifacts: probe, peaks, cover thumbnails. Cached per content hash.
	var mu sync.Mutex
	parallel(len(tracks), func(i int) { s.artifacts(tracks[i], idx, &mu) })

	sort.SliceStable(tracks, func(i, j int) bool {
		a, b := tracks[i], tracks[j]
		if a.Date != b.Date {
			return a.Date > b.Date
		}
		if !a.Updated.Equal(b.Updated) {
			return a.Updated.After(b.Updated)
		}
		return a.Slug < b.Slug
	})
	for _, t := range tracks {
		idx.Tracks = append(idx.Tracks, t)
		idx.BySlug[t.Slug] = t
		for _, a := range t.Audio {
			idx.Media[a.SHA256] = a
		}
		if t.Video != nil {
			idx.Media[t.Video.SHA256] = t.Video
		}
	}
	if err := s.saveState(); err != nil {
		s.Log.Warn("could not save state", "err", err)
	}
	return idx, nil
}

func parallel(n int, fn func(i int)) {
	workers := min(runtime.GOMAXPROCS(0), n)
	ch := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range ch {
				fn(i)
			}
		}()
	}
	for i := 0; i < n; i++ {
		ch <- i
	}
	close(ch)
	wg.Wait()
}

func (s *Scanner) prepare(slug, dir string) *candidate {
	c := &candidate{slug: slug, dir: dir, files: map[string]fileKey{}}
	raw, err := readCapped(filepath.Join(dir, manifest.FileName), 1<<20)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			c.err = errors.New("no track.sig")
		} else {
			c.err = err
		}
		return c
	}
	m, err := manifest.Parse(raw)
	if err != nil {
		c.err = fmt.Errorf("manifest: %w", err)
		return c
	}
	if err := m.Verify(s.Keys); err != nil {
		c.err = fmt.Errorf("manifest: %w", err)
		return c
	}
	c.m, c.raw = m, raw
	listed := map[string]bool{}
	for _, e := range m.Entries {
		listed[e.Name] = true
		fi, err := os.Lstat(filepath.Join(dir, e.Name))
		if err != nil {
			c.err = fmt.Errorf("listed file %s: %w", e.Name, err)
			return c
		}
		if !fi.Mode().IsRegular() {
			c.err = fmt.Errorf("listed file %s is not a regular file", e.Name)
			return c
		}
		c.files[e.Name] = statKey(fi)
		if fi.ModTime().After(c.updated) {
			c.updated = fi.ModTime()
		}
	}
	if ents, err := os.ReadDir(dir); err == nil {
		for _, e := range ents {
			n := e.Name()
			if n != manifest.FileName && !listed[n] && !strings.HasPrefix(n, ".") {
				s.warnOnce("unlisted:"+dir+"/"+n, "ignoring file not in manifest", "slug", slug, "file", n)
			}
		}
	}
	return c
}

func readCapped(p string, limit int64) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if fi.Size() > limit {
		return nil, fmt.Errorf("%s larger than %d bytes", filepath.Base(p), limit)
	}
	b := make([]byte, fi.Size())
	_, err = f.ReadAt(b, 0)
	if err != nil && fi.Size() > 0 {
		return nil, err
	}
	return b, nil
}

// readVerified reads a small listed file and checks it against its hash.
func readVerified(p, want string, limit int64) ([]byte, error) {
	b, err := readCapped(p, limit)
	if err != nil {
		return nil, err
	}
	if sum := sha256Hex(b); sum != want {
		return nil, fmt.Errorf("%s changed since hashing", filepath.Base(p))
	}
	return b, nil
}

func (s *Scanner) assemble(c *candidate) (*Track, error) {
	t := &Track{
		Slug:       c.slug,
		Title:      c.slug,
		Visibility: Public,
		KeyID:      c.m.KeyID,
		Manifest:   c.raw,
		Updated:    c.updated,
	}
	shas := map[string]string{}
	for _, e := range c.m.Entries {
		p := filepath.Join(c.dir, e.Name)
		fs, ok := s.st.Files[p]
		if !ok {
			return nil, fmt.Errorf("could not hash %s", e.Name)
		}
		if fs.SHA256 != e.SHA256 {
			return nil, fmt.Errorf("%s does not match manifest", e.Name)
		}
		shas[e.Name] = e.SHA256
	}
	audio := map[string]*Asset{}
	var coverFile, coverSHA, peaksFile string
	var metaName string
	for _, e := range c.m.Entries {
		role, ext := RoleOf(e.Name)
		p := filepath.Join(c.dir, e.Name)
		k := c.files[e.Name]
		mk := func() *Asset {
			return &Asset{SHA256: e.SHA256, Ext: ext, MIME: MIMEFor(ext), Path: p, Size: k.Size, ModTime: time.Unix(0, k.MtimeNs)}
		}
		switch role {
		case RoleAudio:
			audio[ext] = mk()
		case RoleVideo:
			if t.Video == nil {
				t.Video = mk()
			}
		case RoleCover:
			if coverFile == "" {
				coverFile, coverSHA = p, e.SHA256
			}
		case RoleLyrics:
			b, err := readVerified(p, e.SHA256, 256<<10)
			if err != nil {
				return nil, err
			}
			if !utf8.Valid(b) {
				return nil, errors.New("lyrics.txt is not UTF-8")
			}
			t.Lyrics = strings.TrimSpace(strings.ReplaceAll(string(b), "\r\n", "\n"))
		case RoleMeta:
			if metaName != "" {
				return nil, errors.New("both meta.toml and meta.json present")
			}
			metaName = e.Name
			b, err := readVerified(p, e.SHA256, 64<<10)
			if err != nil {
				return nil, err
			}
			if err := applyMeta(t, e.Name, b); err != nil {
				return nil, fmt.Errorf("%s: %w", e.Name, err)
			}
		case RolePeaks:
			if k.Size == media.PeakBins*2 {
				peaksFile = p
			}
		}
	}
	for _, ext := range audioPref {
		if a := audio[ext]; a != nil {
			t.Audio = append(t.Audio, a)
		}
	}
	if t.Video == nil && len(t.Audio) == 0 {
		return nil, errors.New("no audio or video file listed")
	}
	switch t.Visibility {
	case Draft:
		return nil, nil
	case Public:
	case Unlisted:
		if len(t.Token) < 16 {
			return nil, errors.New("unlisted track needs a token of at least 16 characters (run spor sign)")
		}
	default:
		return nil, fmt.Errorf("unknown visibility %q", t.Visibility)
	}
	t.CoverKey = coverSHA
	if coverFile != "" {
		t.coverSrc = coverFile
	}
	t.peaksSrc = peaksFile
	return t, nil
}

func applyMeta(t *Track, name string, b []byte) error {
	var v conf.Values
	if strings.HasSuffix(name, ".json") {
		raw := map[string]any{}
		if err := json.Unmarshal(b, &raw); err != nil {
			return err
		}
		v = conf.Values{}
		for k, x := range raw {
			if s, ok := x.(string); ok {
				v[k] = s
			}
		}
	} else {
		var err error
		if v, err = conf.Parse(b); err != nil {
			return err
		}
	}
	str := func(key string, dst *string) error {
		s, err := v.String(key, *dst)
		if err == nil {
			*dst = strings.TrimSpace(s)
		}
		return err
	}
	for key, dst := range map[string]*string{
		"title": &t.Title, "date": &t.Date, "created": &t.Created, "style": &t.Style,
		"description": &t.Description, "visibility": &t.Visibility, "token": &t.Token,
	} {
		if err := str(key, dst); err != nil {
			return err
		}
	}
	if t.Title == "" {
		t.Title = t.Slug
	}
	t.Visibility = strings.ToLower(t.Visibility)
	return nil
}

func (s *Scanner) artifacts(t *Track, idx *Index, mu *sync.Mutex) {
	prim := t.Primary()
	// Probe duration and dimensions.
	mu.Lock()
	info, ok := s.st.Media[prim.SHA256]
	mu.Unlock()
	if !ok {
		var err error
		info, err = media.Probe(prim.Path, prim.Ext)
		if err != nil {
			s.Log.Debug("probe failed", "slug", t.Slug, "err", err)
		}
		mu.Lock()
		s.st.Media[prim.SHA256] = info
		mu.Unlock()
	}
	t.Duration, t.Width, t.Height = info.Duration, info.Width, info.Height

	// Peaks: signed peaks.bin from the workstation, else generate if ffmpeg.
	if t.peaksSrc != "" {
		t.PeaksKey = prim.SHA256
		mu.Lock()
		idx.Peaks[t.PeaksKey] = t.peaksSrc
		mu.Unlock()
	} else {
		dir := filepath.Join(s.Cache, prim.SHA256)
		p := filepath.Join(dir, "peaks.bin")
		_, statErr := os.Stat(p)
		mu.Lock()
		prevErr, tried := s.st.Peaks[prim.SHA256]
		mu.Unlock()
		if statErr != nil && (!tried || prevErr == "") && media.HaveFFmpeg() {
			peaks, err := media.Peaks(prim.Path)
			if err == nil {
				err = os.MkdirAll(dir, 0o755)
			}
			if err == nil {
				err = media.WriteFileAtomic(p, peaks, 0o644)
			}
			msg := ""
			if err != nil {
				msg = err.Error()
				s.Log.Warn("peaks failed", "slug", t.Slug, "err", err)
			}
			mu.Lock()
			s.st.Peaks[prim.SHA256] = msg
			mu.Unlock()
			statErr = err
		}
		if statErr == nil {
			t.PeaksKey = prim.SHA256
			mu.Lock()
			idx.Peaks[t.PeaksKey] = p
			mu.Unlock()
		}
	}

	// Cover: signed cover file, else a frame from the video.
	src := t.coverSrc
	if src == "" && t.Video != nil {
		t.CoverKey = t.Video.SHA256
	}
	if t.CoverKey == "" {
		return
	}
	dir := filepath.Join(s.Cache, t.CoverKey)
	mu.Lock()
	cs, known := s.st.Covers[t.CoverKey]
	mu.Unlock()
	paths := map[int]string{}
	have := true
	for _, sz := range CoverSizes {
		paths[sz] = filepath.Join(dir, fmt.Sprintf("cover-%d.jpg", sz))
		if _, err := os.Stat(paths[sz]); err != nil {
			have = false
		}
	}
	if !have && !(known && cs.Err != "") {
		cs = s.makeCovers(t, src, dir, paths)
		mu.Lock()
		s.st.Covers[t.CoverKey] = cs
		mu.Unlock()
		have = cs.Err == ""
	}
	if !have {
		t.CoverKey = ""
		return
	}
	t.CoverW, t.CoverH = cs.W, cs.H
	mu.Lock()
	for sz, p := range paths {
		idx.Covers[fmt.Sprintf("%s-%d", t.CoverKey, sz)] = p
	}
	mu.Unlock()
}

func (s *Scanner) makeCovers(t *Track, src, dir string, paths map[int]string) coverState {
	fail := func(err error) coverState {
		s.Log.Warn("cover failed", "slug", t.Slug, "err", err)
		return coverState{Err: err.Error()}
	}
	var data []byte
	var err error
	if src != "" {
		data, err = readCapped(src, 50<<20)
		if err == nil && sha256Hex(data) != t.CoverKey {
			err = errors.New("cover changed since hashing")
		}
	} else {
		data, err = media.VideoFrame(t.Video.Path)
	}
	if err != nil {
		return fail(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fail(err)
	}
	var cs coverState
	for _, sz := range CoverSizes {
		jpg, w, h, err := media.Thumbnail(data, sz)
		if err != nil {
			return fail(err)
		}
		if err := media.WriteFileAtomic(paths[sz], jpg, 0o644); err != nil {
			return fail(err)
		}
		cs.W, cs.H = w, h // the last (largest) size wins
	}
	return cs
}
