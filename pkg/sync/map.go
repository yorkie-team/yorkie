package sync

import (
	"fmt"
	"sync"
)

// TODO: Temporary mutex map. We will need to replace this with distributed lock later.
//  - https://github.com/minio/dsync#other-techniques
type Map struct {
	muMap *sync.Map
}

func NewMap() *Map {
	return &Map{
		muMap: &sync.Map{},
	}
}

func (m *Map) Unlock(key string) error {
	val, ok := m.muMap.Load(key)
	if !ok {
		return fmt.Errorf("unlocked mutex: %s", key)
	}
	mu := val.(*sync.Mutex)
	m.muMap.Delete(key)
	mu.Unlock()

	return nil
}

func (m *Map) Lock(key string) error {
	mu := sync.Mutex{}
	val, _ := m.muMap.LoadOrStore(key, &mu)
	mm := val.(*sync.Mutex)
	mm.Lock()
	if mm != &mu {
		mm.Unlock()
		return m.Lock(key)
	}

	return nil
}
