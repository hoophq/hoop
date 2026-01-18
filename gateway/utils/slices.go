package utils

import "slices"

func SlicesHasIntersection[T comparable](a, b []T) bool {
	return SlicesFindFirstIntersection(a, b) != nil
}

func SlicesFindFirstIntersection[T comparable](a, b []T) *T {
	// Ensure 'a' is the smaller slice to optimize performance
	if len(a) > len(b) {
		a, b = b, a
	}

	index := slices.IndexFunc(a, func(x T) bool {
		return slices.Contains(b, x)
	})
	if index == -1 {
		return nil
	}
	return &a[index]
}
