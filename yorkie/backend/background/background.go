package background

import (
	"sync"

	"github.com/yorkie-team/yorkie/yorkie/log"
)

// Background is the background service.
type Background struct {
	// closing is closed by backend close.
	closing chan struct{}

	// wgMu blocks concurrent WaitGroup mutation while backend closing
	wgMu sync.RWMutex

	// wg is used to wait for the goroutines that depends on the backend state
	// to exit when closing the backend.
	wg sync.WaitGroup
}

// New creates a new background service.
func New() *Background {
	return &Background{
		closing: make(chan struct{}),
	}
}

// AttachGoroutine creates a goroutine on a given function and tracks it using
// the background's WaitGroup.
func (b *Background) AttachGoroutine(f func()) {
	b.wgMu.RLock() // this blocks with ongoing close(b.closing)
	defer b.wgMu.RUnlock()
	select {
	case <-b.closing:
		log.Logger.Warn("backend has closed; skipping AttachGoroutine")
		return
	default:
	}

	// now safe to add since WaitGroup wait has not started yet
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		f()
	}()
}

// Close closes the background service.
func (b *Background) Close() {
	b.wgMu.Lock()
	close(b.closing)
	b.wgMu.Unlock()

	// wait for goroutines before closing backend
	b.wg.Wait()
}
