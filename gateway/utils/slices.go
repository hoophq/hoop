package utils

import "slices"

func SlicesHasIntersection[T comparable](a, b []T) bool {
	// Ensure 'a' is the smaller slice to optimize performance
	if len(a) > len(b) {
		a, b = b, a
	}

	return slices.ContainsFunc(a, func(x T) bool {
		return slices.Contains(b, x)
	})
}
