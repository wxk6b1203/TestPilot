package httpserver

import (
	"sync"
	"time"
)

// 登录限流：内存固定窗口（按来源 IP）。
// 限制暴力破解频率；多副本部署时各实例独立计数（够用，反代层可做更强限制）。
var loginLimit = newFixedWindowLimiter(10, time.Minute)
var registerLimit = newFixedWindowLimiter(10, time.Hour) // 自助建租户比登录更重，小时级限流

type fixedWindowLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	hits   map[string]windowEntry
}

type windowEntry struct {
	start time.Time
	n     int
}

func newFixedWindowLimiter(limit int, window time.Duration) *fixedWindowLimiter {
	return &fixedWindowLimiter{
		limit: limit, window: window,
		hits: map[string]windowEntry{},
	}
}

// Allow 判断 key 是否还有配额；并周期性清理过期条目防 map 无界增长。
func (l *fixedWindowLimiter) Allow(key string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.hits) > 10_000 {
		for k, e := range l.hits {
			if now.Sub(e.start) > l.window {
				delete(l.hits, k)
			}
		}
	}
	e, ok := l.hits[key]
	if !ok || now.Sub(e.start) > l.window {
		l.hits[key] = windowEntry{start: now, n: 1}
		return true
	}
	if e.n >= l.limit {
		return false
	}
	e.n++
	l.hits[key] = e
	return true
}
