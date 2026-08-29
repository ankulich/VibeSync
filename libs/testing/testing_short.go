// Stub for std testing.Short() without importing the std testing package at
// the top of testing.go (which would shadow our package name). Kept separate
// so the rule is obvious.

//go:build !without_testing_short

package testing

import stdtesting "testing"

func init() {
	testingShort = stdtesting.Short
}
