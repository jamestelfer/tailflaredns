package seq

import (
	"iter"
	"slices"
)

// import (
// 	"slices"
// 	"maps"
// )

func SelectSlice[E any, F any](slice []E, transformer func(E) F) []F {
	return slices.Collect(
		Select(slices.Values(slice), transformer),
	)
}

func Select[E any, F any](seq iter.Seq[E], transformer func(E) F) iter.Seq[F] {
	return func(yield func(F) bool) {
		for v := range seq {
			if !yield(transformer(v)) {
				return
			}
		}
	}
}

func Select2[K any, V any, K2 any, V2 any](seq iter.Seq2[K, V], transformer func(K, V) (K2, V2)) iter.Seq2[K2, V2] {
	return func(yield func(K2, V2) bool) {
		for k, v := range seq {
			if !yield(transformer(k, v)) {
				return
			}
		}
	}
}

func ToMap[T any, K comparable](slice iter.Seq[T], keyFunc func(T) K) map[K]T {
	m := make(map[K]T)

	for v := range slice {
		m[keyFunc(v)] = v
	}

	return m
}
