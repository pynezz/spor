package server

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pynezz/spor/internal/conf"
)

// Config is the server configuration.
type Config struct {
	Listen         string
	Library        string
	Cache          string
	PublicURL      string
	PublicKeys     []string
	RescanInterval time.Duration
	SWCacheLimitMB int64
	TrustProxy     bool    // honour X-Forwarded-For from loopback peers
	MediaRate      float64 // media requests per second per IP
	MediaBurst     int
	PlayLog        bool
	Likes          bool
}

// DefaultConfig returns defaults matching the plan.
func DefaultConfig() Config {
	return Config{
		Listen:         "127.0.0.1:8740",
		Library:        "/srv/spor/library",
		Cache:          "/srv/spor/cache",
		PublicURL:      "https://lytt.txtv.no",
		RescanInterval: 60 * time.Second,
		SWCacheLimitMB: 500,
		TrustProxy:     true,
		MediaRate:      10,
		MediaBurst:     60,
		PlayLog:        true,
		Likes:          true,
	}
}

// LoadConfig reads a flat TOML config on top of the defaults.
func LoadConfig(path string) (Config, error) {
	c := DefaultConfig()
	v, err := conf.ParseFile(path)
	if err != nil {
		return c, err
	}
	known := map[string]bool{}
	str := func(k string, dst *string) {
		known[k] = true
		if err == nil {
			*dst, err = v.String(k, *dst)
		}
	}
	str("listen", &c.Listen)
	str("library", &c.Library)
	str("cache", &c.Cache)
	str("public_url", &c.PublicURL)
	var keys, interval string
	str("public_keys", &keys)
	str("rescan_interval", &interval)
	known["sw_cache_limit_mb"], known["trust_proxy"], known["media_rate"], known["media_burst"], known["play_log"], known["likes"] = true, true, true, true, true, true
	if err == nil {
		c.SWCacheLimitMB, err = v.Int("sw_cache_limit_mb", c.SWCacheLimitMB)
	}
	if err == nil {
		c.TrustProxy, err = v.Bool("trust_proxy", c.TrustProxy)
	}
	if err == nil {
		c.PlayLog, err = v.Bool("play_log", c.PlayLog)
	}
	if err == nil {
		c.Likes, err = v.Bool("likes", c.Likes)
	}
	var rate, burst int64
	if err == nil {
		rate, err = v.Int("media_rate", int64(c.MediaRate))
		c.MediaRate = float64(rate)
	}
	if err == nil {
		burst, err = v.Int("media_burst", int64(c.MediaBurst))
		c.MediaBurst = int(burst)
	}
	if err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	for k := range v {
		if !known[k] {
			return c, fmt.Errorf("%s: unknown key %q", path, k)
		}
	}
	if keys != "" {
		for _, k := range strings.Split(keys, ",") {
			if k = strings.TrimSpace(k); k != "" {
				c.PublicKeys = append(c.PublicKeys, k)
			}
		}
	}
	if interval != "" {
		if c.RescanInterval, err = time.ParseDuration(interval); err != nil {
			return c, fmt.Errorf("%s: rescan_interval: %w", path, err)
		}
	}
	return c, nil
}

// Finish fills derived defaults and validates.
func (c *Config) Finish() error {
	c.PublicURL = strings.TrimRight(c.PublicURL, "/")
	u, err := url.Parse(c.PublicURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fmt.Errorf("public_url must be an absolute http(s) URL")
	}
	if len(c.PublicKeys) == 0 {
		// systemd LoadCredential= or a podman secret.
		for _, p := range []string{
			filepath.Join(os.Getenv("CREDENTIALS_DIRECTORY"), "spor.pub"),
			"/run/secrets/spor.pub",
		} {
			if _, err := os.Stat(p); err == nil && !strings.HasPrefix(p, "spor.pub") {
				c.PublicKeys = []string{p}
				break
			}
		}
	}
	if len(c.PublicKeys) == 0 {
		return fmt.Errorf("no public key configured (public_keys, $CREDENTIALS_DIRECTORY/spor.pub or /run/secrets/spor.pub)")
	}
	if c.RescanInterval < 0 {
		c.RescanInterval = 0
	}
	if c.MediaBurst < 1 {
		c.MediaBurst = 1
	}
	return nil
}
