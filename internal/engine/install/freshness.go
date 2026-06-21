package install

import (
	"fmt"
	"time"
)

// PinSetDate is when the embedded DesiredState pins were last verified + updated (YYYY-MM-DD). It is
// the freshness anchor: bump it whenever you edit a pin's version/sha256/url. A stale anchor means
// safeslop can only install OLD (pinned) tool versions — still checksum-verified and safe, but
// possibly missing upstream security fixes — so Freshness surfaces an advisory "update safeslop"
// warning, never a block (specs/0036 Task 9).
const PinSetDate = "2026-06-21"

// freshnessFloorDays is the age past which the pin set is flagged stale. ~a quarter: long enough to
// avoid nagging on a normal release cadence, short enough that several missed tool releases trip it.
const freshnessFloorDays = 90

// FreshnessReport describes how old the embedded pin set is relative to the floor.
type FreshnessReport struct {
	PinSetDate string `json:"pin_set_date"`
	AgeDays    int    `json:"age_days"`
	FloorDays  int    `json:"floor_days"`
	Stale      bool   `json:"stale"`
}

// Freshness reports the age of the embedded pin set as of now. A malformed PinSetDate yields a
// non-stale report (fail-open on the advisory: a bad date must never spuriously warn).
func Freshness(now time.Time) FreshnessReport {
	r := FreshnessReport{PinSetDate: PinSetDate, FloorDays: freshnessFloorDays}
	set, err := time.Parse("2006-01-02", PinSetDate)
	if err != nil {
		return r
	}
	age := int(now.Sub(set).Hours() / 24)
	if age < 0 {
		age = 0 // a clock skew into the past is not "negative age"
	}
	r.AgeDays = age
	r.Stale = age > freshnessFloorDays
	return r
}

// Message returns the advisory warning when the pin set is stale, else "".
func (f FreshnessReport) Message() string {
	if !f.Stale {
		return ""
	}
	return fmt.Sprintf("safeslop's tool pins were set %d days ago (floor %d) — update safeslop for current, patched tool versions",
		f.AgeDays, f.FloorDays)
}
