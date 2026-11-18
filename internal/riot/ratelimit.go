package riot

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Window is one rate limit bucket, e.g. 100 requests per 2 minutes.
type Window struct {
	Limit int
	Per   time.Duration
}

// PersonalKeyLimits are the documented limits for dev and personal keys.
var PersonalKeyLimits = []Window{{Limit: 20, Per: time.Second}, {Limit: 100, Per: 2 * time.Minute}}

// ParseLimits parses "20/1s,100/2m" into windows.
func ParseLimits(s string) ([]Window, error) {
	var out []Window
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		n, d, ok := strings.Cut(part, "/")
		if !ok {
			return nil, fmt.Errorf("bad limit %q, want N/duration", part)
		}
		limit, err := strconv.Atoi(n)
		if err != nil || limit <= 0 {
			return nil, fmt.Errorf("bad limit count in %q", part)
		}
		per, err := time.ParseDuration(d)
		if err != nil || per <= 0 {
			return nil, fmt.Errorf("bad limit duration in %q", part)
		}
		out = append(out, Window{Limit: limit, Per: per})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no limits in %q", s)
	}
	return out, nil
}

// Limiter enforces several sliding windows at once. It is conservative: one
// limiter is shared across all hosts even though Riot counts per region.
type Limiter struct {
	mu      sync.Mutex
	windows []Window
	logs    [][]time.Time
	now     func() time.Time
	pad     time.Duration
}

func NewLimiter(windows []Window) *Limiter {
	return &Limiter{
		windows: windows,
		logs:    make([][]time.Time, len(windows)),
		now:     time.Now,
		pad:     50 * time.Millisecond,
	}
}

// Wait blocks until a request may be sent, then records it.
func (l *Limiter) Wait(ctx context.Context) error {
	for {
		l.mu.Lock()
		now := l.now()
		var wait time.Duration
		for i, w := range l.windows {
			log := l.logs[i]
			cut := 0
			for cut < len(log) && now.Sub(log[cut]) >= w.Per {
				cut++
			}
			log = log[cut:]
			l.logs[i] = log
			if len(log) >= w.Limit {
				if d := log[0].Add(w.Per).Sub(now) + l.pad; d > wait {
					wait = d
				}
			}
		}
		if wait == 0 {
			for i := range l.windows {
				l.logs[i] = append(l.logs[i], now)
			}
			l.mu.Unlock()
			return nil
		}
		l.mu.Unlock()

		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}
