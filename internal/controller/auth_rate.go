package controller

import (
	"net"
	"net/netip"
	"sync"
	"time"
)

const (
	loginWindow   = time.Minute
	loginPerIP    = 5
	loginTotal    = 30
	loginParallel = 2
)

type loginLimiter struct {
	mu      sync.Mutex
	until   time.Time
	counts  map[netip.Addr]int
	total   int
	running int
}

// begin reserves a login attempt and a bcrypt slot. The global limit also
// bounds the number of distinct addresses stored in each window.
func (l *loginLimiter) begin(remote string, now time.Time) time.Duration {
	host, _, _ := net.SplitHostPort(remote)
	address, _ := netip.ParseAddr(host)
	address = address.Unmap().WithZone("")
	l.mu.Lock()
	defer l.mu.Unlock()
	if !now.Before(l.until) {
		l.until = now.Add(loginWindow)
		l.counts = make(map[netip.Addr]int)
		l.total = 0
	}
	if l.total >= loginTotal || l.counts[address] >= loginPerIP {
		return l.until.Sub(now)
	}
	if l.running >= loginParallel {
		return time.Second
	}
	l.counts[address]++
	l.total++
	l.running++
	return 0
}

func (l *loginLimiter) end() {
	l.mu.Lock()
	l.running--
	l.mu.Unlock()
}
