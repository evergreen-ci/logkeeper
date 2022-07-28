package storage

import (
	"time"
)

type TimeRange struct {
	StartAt time.Time `json:"start" yaml:"start"`
	EndAt   time.Time `json:"end" yaml:"end"`
}

func (t TimeRange) Duration() time.Duration { return t.EndAt.Sub(t.StartAt) }
func (t TimeRange) IsZero() bool            { return t.EndAt.IsZero() && t.StartAt.IsZero() }
func (t TimeRange) IsValid() bool           { return t.Duration() >= 0 }

// Check returns true if the given time is within the TimeRange (inclusive) and
// false otherwise.
func (t TimeRange) Check(ts time.Time) bool {
	if (ts.After(t.StartAt) || ts.Equal(t.StartAt)) &&
		(ts.Before(t.EndAt) || ts.Equal(t.EndAt)) {
		return true
	}
	return false
}

// Check returns true if this TimeRange overlaps the other TimeRange
func (t TimeRange) Intersects(other TimeRange) bool {
	if t.EndAt.Before(other.StartAt) || t.StartAt.After(other.EndAt) {
		return false
	}
	return true
}

// GetTimeRange builds a time range structure. If startAt is the zero
// time, then end defaults to the current time and the start time is
// determined by the duration. Otherwise the end time is determined
// using the duration.
func GetTimeRange(startAt time.Time, duration time.Duration) TimeRange {
	var endTime time.Time

	if startAt.IsZero() {
		endTime = time.Now()
		startAt = endTime.Add(-duration)
	} else {
		endTime = startAt.Add(duration)
	}

	return TimeRange{
		StartAt: startAt,
		EndAt:   endTime,
	}
}
