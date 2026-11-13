package repository

import (
	"testing"
	"time"
)

func TestOrNow(t *testing.T) {
	set := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if got := orNow(set); !got.Equal(set) {
		t.Errorf("orNow(%s) = %s, want it unchanged", set, got)
	}
	before := time.Now().Add(-time.Second)
	if got := orNow(time.Time{}); got.Before(before) {
		t.Errorf("orNow(zero) = %s, want the current time", got)
	}
}
