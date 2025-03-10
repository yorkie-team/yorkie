package limit

import "time"

// Bucket represents a single-token bucket that refills every specified time window.
type Bucket struct {
	window time.Duration // The interval at which the bucket refills.
	last   time.Time     // The last time a token was granted.
}

// NewBucket creates a new Bucket with the given initial time and refill window.
func NewBucket(now time.Time, window time.Duration) Bucket {
	return Bucket{
		window: window,
		last:   now,
	}
}

// Allow checks if a token can be granted at the given time.
// It returns true if the time has advanced past the refill window, otherwise false.
func (b *Bucket) Allow(now time.Time) bool {
	if now.Before(b.last.Add(b.window)) {
		return false
	}

	b.last = now
	return true
}
