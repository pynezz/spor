package server

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// limiter is a per-IP token bucket. It is generous on purpose: seeking in a
// track issues a burst of Range requests, and that must never be throttled.
type limiter struct {
	rate  float64
	burst float64
	mu    sync.Mutex
	b     map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newLimiter(rate float64, burst int) *limiter {
	return &limiter{rate: rate, burst: float64(burst), b: map[string]*bucket{}}
}

func (l *limiter) allow(ip string) bool {
	if l.rate <= 0 {
		return true
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.b[ip]
	if b == nil {
		b = &bucket{tokens: l.burst, last: now}
		l.b[ip] = b
	}
	b.tokens = min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// gc drops buckets that have been full for a while.
func (l *limiter) gc() {
	cutoff := time.Now().Add(-10 * time.Minute)
	l.mu.Lock()
	defer l.mu.Unlock()
	for ip, b := range l.b {
		if b.last.Before(cutoff) {
			delete(l.b, ip)
		}
	}
}

// clientIP returns the peer address, or the right-most X-Forwarded-For entry
// when the peer is a trusted loopback proxy (Caddy on the same host).
func (s *Server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if s.cfg.TrustProxy {
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
				parts := strings.Split(xff, ",")
				if last := strings.TrimSpace(parts[len(parts)-1]); last != "" {
					host = last
				}
			}
		}
	}
	// Bucket IPv6 clients per /64, which is what one household usually gets.
	if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
		return ip.Mask(net.CIDRMask(64, 128)).String()
	}
	return host
}
