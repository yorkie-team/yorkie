/*
 * Copyright 2024 The Yorkie Authors. All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package cmap_test

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/cmap"
)

func TestMap(t *testing.T) {
	t.Run("set and get", func(t *testing.T) {
		m := cmap.New[string, int]()

		m.Set("a", 1)
		v, exists := m.Get("a")
		assert.True(t, exists)
		assert.Equal(t, 1, v)

		v, exists = m.Get("b")
		assert.False(t, exists)
		assert.Equal(t, 0, v)
	})

	t.Run("upsert", func(t *testing.T) {
		m := cmap.New[string, int]()

		v := m.Upsert("a", func(val int, exists bool) int {
			if exists {
				return val + 1
			}
			return 1
		})
		assert.Equal(t, 1, v)

		v = m.Upsert("a", func(val int, exists bool) int {
			if exists {
				return val + 1
			}
			return 1
		})
		assert.Equal(t, 2, v)
	})

	t.Run("delete", func(t *testing.T) {
		m := cmap.New[string, int]()

		m.Set("a", 1)
		exists := m.Delete("a", func(val int, exists bool) bool {
			assert.Equal(t, 1, val)
			return exists
		})
		assert.True(t, exists)

		_, exists = m.Get("a")
		assert.False(t, exists)
	})
}

func randomIntn(n int) int {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return int(binary.LittleEndian.Uint64(b) % uint64(n))
}

func TestConcurrentMap(t *testing.T) {
	t.Run("concurrent access", func(t *testing.T) {
		m := cmap.New[int, int]()
		const numRoutines = 100
		const numOperations = 10000

		var wg sync.WaitGroup
		wg.Add(numRoutines)

		for i := 0; i < numRoutines; i++ {
			go func(routineID int) {
				defer wg.Done()
				for j := 0; j < numOperations; j++ {
					key := randomIntn(1000)
					value := routineID*numOperations + j

					switch randomIntn(3) {
					case 0: // Set
						m.Set(key, value)
					case 1: // Get
						_, _ = m.Get(key)
					case 2: // Delete
						m.Delete(key, func(val int, exists bool) bool {
							return exists
						})
					}
				}
			}(i)
		}

		wg.Wait()

		// Verify the final state
		if m.Len() > 1000 {
			t.Errorf("Map length (%d) is greater than maximum possible unique keys (1000)", m.Len())
		}
	})

	t.Run("concurrent set and get", func(t *testing.T) {
		m := cmap.New[string, int]()
		const numRoutines = 50
		const numOperations = 100

		var wg sync.WaitGroup
		wg.Add(numRoutines * 2)

		// Start setter routines
		for i := 0; i < numRoutines; i++ {
			go func(routineID int) {
				defer wg.Done()
				for j := 0; j < numOperations; j++ {
					key := fmt.Sprintf("key-%d-%d", routineID, j)
					m.Set(key, j)
				}
			}(i)
		}

		// Start getter routines
		for i := 0; i < numRoutines; i++ {
			go func(routineID int) {
				defer wg.Done()
				for j := 0; j < numOperations; j++ {
					key := fmt.Sprintf("key-%d-%d", routineID, j)
					for {
						if value, ok := m.Get(key); ok && value == j {
							break
						}
						time.Sleep(time.Microsecond) // Small delay before retry
					}
				}
			}(i)
		}

		wg.Wait()

		expectedLen := numRoutines * numOperations
		if m.Len() != expectedLen {
			t.Errorf("Expected map length %d, but got %d", expectedLen, m.Len())
		}
	})

	t.Run("concurrent iteration", func(t *testing.T) {
		m := cmap.New[int, int]()
		const numItems = 10000

		// Populate the map
		for i := 0; i < numItems; i++ {
			m.Set(i, i)
		}

		var wg sync.WaitGroup
		wg.Add(3) // For Keys, Values, and modifier goroutines

		// Start a goroutine to continuously modify the map.
		go func() {
			defer wg.Done()
			for i := 0; i < numItems; i++ {
				m.Set(randomIntn(numItems), i)
				m.Delete(randomIntn(numItems), func(val int, exists bool) bool {
					return exists
				})
			}
		}()

		// Start a goroutine to iterate over keys.
		go func() {
			defer wg.Done()
			keys := m.Keys()
			if len(keys) > numItems {
				t.Errorf("Number of keys (%d) is greater than inserted items (%d)", len(keys), numItems)
			}
		}()

		// Start a goroutine to iterate over values.
		go func() {
			defer wg.Done()
			values := m.Values()
			if len(values) > numItems {
				t.Errorf("Number of values (%d) is greater than inserted items (%d)", len(values), numItems)
			}
		}()

		wg.Wait()
	})
}
