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

var TimeRangeMin time.Time = time.Time{}

// Chosen because it is effectively infinitely far in the future but UnixNano only works through 2262
var TimeRangeMax time.Time = time.Date(2200, 1, 1, 1, 0, 0, 0, time.UTC)

// Creates a new time range. Use TimeRangeMin and TimeRangeMax for "open ended" time ranges
func NewTimeRange(start time.Time, end time.Time) TimeRange {
	return TimeRange{
		StartAt: start,
		EndAt:   end,
	}
}

// Check returns true if this TimeRange overlaps the other TimeRange
func (t TimeRange) Intersects(other TimeRange) bool {
	if t.EndAt.Before(other.StartAt) || t.StartAt.After(other.EndAt) {
		return false
	}
	return true
}
