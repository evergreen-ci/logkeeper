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

// Check returns true if this TimeRange overlaps the other TimeRange
func (t TimeRange) Intersects(other TimeRange) bool {
	if t.EndAt.Before(other.StartAt) || t.StartAt.After(other.EndAt) {
		return false
	}
	return true
}
