package inspect

import "time"

type MetricsLogger interface {
	LogLookup(name string, duration time.Duration, timestamp *time.Time, err bool)
}

type StubMetrics struct{}

func (s *StubMetrics) LogLookup(string, time.Duration, *time.Time, bool) {
	// No-op
}
