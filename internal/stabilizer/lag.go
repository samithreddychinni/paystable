package stabilizer

import (
	"sort"
	"sync"
	"time"
)

// LagEstimator records the delay between a webhook and the matching gateway status.
type LagEstimator struct {
	mu        sync.RWMutex
	maxSample int
	minSample int
	prior     defaults
	samples   map[string][]time.Duration
}

type defaults struct {
	p50, p75, p90, p99 time.Duration
}

// NewLagEstimator creates an estimator with safe polling defaults.
func NewLagEstimator() *LagEstimator {
	return &LagEstimator{
		maxSample: 500,
		minSample: 50,
		prior: defaults{
			p50: 10 * time.Second,
			p75: 30 * time.Second,
			p90: 60 * time.Second,
			p99: 180 * time.Second,
		},
		samples: make(map[string][]time.Duration),
	}
}

// Record stores a delay and keeps the most recent samples.
func (e *LagEstimator) Record(gateway string, lag time.Duration) {
	if lag < 0 {
		lag = 0
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	s := append(e.samples[gateway], lag)
	if len(s) > e.maxSample {
		s = s[len(s)-e.maxSample:]
	}
	e.samples[gateway] = s
}

// Schedule defines bounded polling times for a gateway.
type Schedule struct {
	CatchPolls []time.Duration
	FailAfter  time.Duration
}

// ScheduleFor returns the polling schedule for a gateway.
func (e *LagEstimator) ScheduleFor(gateway string) Schedule {
	// Copy the samples while the read lock prevents concurrent changes.
	e.mu.RLock()
	s := make([]time.Duration, len(e.samples[gateway]))
	copy(s, e.samples[gateway])
	e.mu.RUnlock()

	var p50, p75, p90, p99 time.Duration
	if len(s) < e.minSample {
		p50, p75, p90, p99 = e.prior.p50, e.prior.p75, e.prior.p90, e.prior.p99
	} else {
		sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
		p50 = quantile(s, 0.50)
		p75 = quantile(s, 0.75)
		p90 = quantile(s, 0.90)
		p99 = quantile(s, 0.99)
	}

	return Schedule{
		CatchPolls: []time.Duration{p50, p75, p90},
		FailAfter:  p99,
	}
}

func quantile(sorted []time.Duration, q float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(q * float64(len(sorted)))
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}

// SampleCount returns the sample count for a gateway.
func (e *LagEstimator) SampleCount(gateway string) int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.samples[gateway])
}
