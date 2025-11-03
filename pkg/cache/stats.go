package cache

import (
	"sync/atomic"
)

// Stats holds cache statistics.
type Stats struct {
	hits   int64
	misses int64
}

// Hits returns the number of cache hits.
func (s *Stats) Hits() int64 {
	return atomic.LoadInt64(&s.hits)
}

// Misses returns the number of cache misses.
func (s *Stats) Misses() int64 {
	return atomic.LoadInt64(&s.misses)
}

// Total returns the total number of cache operations.
func (s *Stats) Total() int64 {
	return s.Hits() + s.Misses()
}

// HitRate returns the cache hit rate as a percentage (0-100).
func (s *Stats) HitRate() float64 {
	total := s.Total()
	if total == 0 {
		return 0.0
	}
	return float64(s.Hits()) / float64(total) * 100.0
}

// Reset resets all statistics to zero.
func (s *Stats) Reset() {
	atomic.StoreInt64(&s.hits, 0)
	atomic.StoreInt64(&s.misses, 0)
}
