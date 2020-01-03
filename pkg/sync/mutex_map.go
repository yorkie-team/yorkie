package sync

import (
	"errors"
	"sync"
)

var (
	errAlreadyUnlockedKey = errors.New("already unlocked key")
)

// TODO: Temporary mutex map.
//  - We will need to replace this with distributed lock later.
//  - https://github.com/minio/dsync#other-techniques
type MutexMap struct {
	mutexMap *sync.Map
}

func NewMutexMap() *MutexMap {
	return &MutexMap{
		mutexMap: &sync.Map{},
	}
}

func (m *MutexMap) Unlock(key string) error {
	value, ok := m.mutexMap.Load(key)
	if !ok {
		return errAlreadyUnlockedKey
	}

	mu := value.(*sync.Mutex)
	m.mutexMap.Delete(key)
	mu.Unlock()

	return nil
}

func (m *MutexMap) Lock(key string) error {
	mu := sync.Mutex{}
	value, _ := m.mutexMap.LoadOrStore(key, &mu)
	loadedMu := value.(*sync.Mutex)
	loadedMu.Lock()
	if loadedMu != &mu {
		loadedMu.Unlock()
		return m.Lock(key)
	}

	return nil
}
