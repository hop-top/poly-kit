package util

import "fmt"

// Do panics if err is non-nil, otherwise returns v.
func Do[T any](v T, err error) T {
	if err != nil {
		panic(fmt.Sprintf("must: %v", err))
	}
	return v
}

// OK panics if err is non-nil.
func OK(err error) {
	if err != nil {
		panic(fmt.Sprintf("must: %v", err))
	}
}
