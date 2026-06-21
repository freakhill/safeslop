package install

import (
	"testing"
	"time"
)

func TestFreshness(t *testing.T) {
	set, err := time.Parse("2006-01-02", PinSetDate)
	if err != nil {
		t.Fatalf("PinSetDate %q must be a YYYY-MM-DD date: %v", PinSetDate, err)
	}

	// Just after the pin date: fresh, no warning.
	fresh := Freshness(set.AddDate(0, 0, 10))
	if fresh.Stale || fresh.Message() != "" {
		t.Fatalf("10 days old must be fresh, got %+v msg=%q", fresh, fresh.Message())
	}
	if fresh.AgeDays != 10 {
		t.Fatalf("age = %d, want 10", fresh.AgeDays)
	}

	// Well past the floor: stale, with an advisory message that says nothing about blocking.
	stale := Freshness(set.AddDate(0, 0, 200))
	if !stale.Stale {
		t.Fatalf("200 days old must be stale, got %+v", stale)
	}
	if msg := stale.Message(); msg == "" {
		t.Fatal("a stale report must carry a warning message")
	}

	// A clock that reads before the pin date must not produce a negative age or a false stale.
	skew := Freshness(set.AddDate(0, 0, -5))
	if skew.AgeDays != 0 || skew.Stale {
		t.Fatalf("past-skewed clock must clamp to fresh age 0, got %+v", skew)
	}
}
