package background

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/yorkie-team/yorkie/yorkie/logging"
)

type routineID int32

func (c *routineID) next() string {
	next := atomic.AddInt32((*int32)(c), 1)
	return "b" + strconv.Itoa(int(next))
}

// Background is the background service. It is responsible for managing
// background routines.
type Background struct {
	// closing is closed by backend close.
	closing chan struct{}

	// wgMu blocks concurrent WaitGroup mutation while backend closing
	wgMu sync.RWMutex

	// wg is used to wait for the goroutines that depends on the backend state
	// to exit when closing the backend.
	wg sync.WaitGroup

	// routineID is used to generate routine ID.
	routineID routineID
}

// New creates a new background service.
func New() *Background {
	return &Background{
		closing: make(chan struct{}),
	}
}

// AttachGoroutine creates a goroutine on a given function and tracks it using
// the background's WaitGroup.
func (b *Background) AttachGoroutine(f func(ctx context.Context)) {
	b.wgMu.RLock() // this blocks with ongoing close(b.closing)
	defer b.wgMu.RUnlock()
	select {
	case <-b.closing:
		logging.DefaultLogger().Warn("backend has closed; skipping AttachGoroutine")
		return
	default:
	}

	// now safe to add since WaitGroup wait has not started yet
	b.wg.Add(1)
	routineLogger := logging.New(b.routineID.next())
	go func() {
		defer b.wg.Done()
		f(logging.With(context.Background(), routineLogger))
	}()
}

// Close closes the background service. This will wait for all goroutines to
// exit.
func (b *Background) Close() {
	b.wgMu.Lock()
	close(b.closing)
	b.wgMu.Unlock()

	// wait for goroutines before closing backend
	b.wg.Wait()
}
