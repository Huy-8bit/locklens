//go:build !cgo

package sqlparse

import (
	"errors"
	"testing"
)

func TestWithoutCgo(t *testing.T) {
	if Available() {
		t.Fatal("a build without cgo has no parser")
	}
	if _, err := Analyze("SELECT 1"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}
