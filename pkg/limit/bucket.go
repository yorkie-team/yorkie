package limit

import "time"

// Bucket is one token bucket that charged every window
type Bucket struct {
	window time.Duration
	// last is the last time the limiter's tokens field was updated
	last time.Time
}

func NewBucket(now time.Time, window time.Duration) Bucket {
	return Bucket{
		window: window,
		last:   now,
	}
}

func (b *Bucket) Allow(now time.Time) bool {
	if now.Before(b.last.Add(b.window)) {
		return false
	}

	b.last = now
	return true
}
