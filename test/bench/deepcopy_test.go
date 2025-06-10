package bench

import (
	"fmt"
	"testing"
)

func genMap(n int) map[string]string {
	m := make(map[string]string, n)
	for i := 0; i < n; i++ {
		m[fmt.Sprintf("k%d", i)] = fmt.Sprintf("v%d", i)
	}
	return m
}

func genMapInt(n int) map[int]int {
	m := make(map[int]int, n)
	for i := 0; i < n; i++ {
		m[i] = i
	}
	return m
}

func genSlice(n int) []struct{ K, V string } {
	s := make([]struct{ K, V string }, n)
	for i := 0; i < n; i++ {
		s[i] = struct{ K, V string }{
			K: fmt.Sprintf("k%d", i),
			V: fmt.Sprintf("v%d", i),
		}
	}
	return s
}

func BenchmarkDeepCopySlice100(b *testing.B) {
	s := genSlice(100)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = deepCopySlice(s)
	}
}

func BenchmarkDeepCopySlice1000(b *testing.B) {
	s := genSlice(1000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = deepCopySlice(s)
	}
}

func BenchmarkDeepCopySlice10000(b *testing.B) {
	s := genSlice(10000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = deepCopySlice(s)
	}
}

func BenchmarkDeepCopyMap100(b *testing.B) {
	m := genMap(100)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = deepCopyMap(m)
	}
}

func BenchmarkDeepCopyMap1000(b *testing.B) {
	m := genMap(1000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = deepCopyMap(m)
	}
}

func BenchmarkDeepCopyMap10000(b *testing.B) {
	m := genMap(10000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = deepCopyMap(m)
	}
}

func BenchmarkDeepCopyMapInt100(b *testing.B) {
	m := genMapInt(100)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = deepCopyMapInt(m)
	}
}

func BenchmarkDeepCopyMapInt1000(b *testing.B) {
	m := genMapInt(1000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = deepCopyMapInt(m)
	}
}
func BenchmarkDeepCopyMapInt10000(b *testing.B) {
	m := genMapInt(10000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = deepCopyMapInt(m)
	}
}

// map[string]string deep copy
func deepCopyMap(m map[string]string) map[string]string {
	copy := make(map[string]string, len(m))
	for k, v := range m {
		copy[k] = v
	}
	return copy
}

// map[string]string deep copy
func deepCopyMapInt(m map[int]int) map[int]int {
	copy := make(map[int]int, len(m))
	for k, v := range m {
		copy[k] = v
	}
	return copy
}

// []struct{string, string} deep copy
func deepCopySlice(s []struct{ K, V string }) []struct{ K, V string } {
	copy := make([]struct{ K, V string }, len(s))
	copy = append(copy[:0], s...)
	return copy
}
