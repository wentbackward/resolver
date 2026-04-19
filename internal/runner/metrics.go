package runner

import (
	"math"
	"sort"
)

// TimingAggregate matches scorecard.summary.timing and per-tier timing
// shapes. All fields in milliseconds except count.
type TimingAggregate struct {
	TotalMs int64 `json:"totalMs"`
	AvgMs   int64 `json:"avgMs"`
	P50Ms   int64 `json:"p50Ms"`
	P95Ms   int64 `json:"p95Ms,omitempty"`
	MaxMs   int64 `json:"maxMs,omitempty"`
	Count   int   `json:"count"`
}

// Timings aggregates a list of per-query elapsed times.
func Timings(samples []int64) TimingAggregate {
	if len(samples) == 0 {
		return TimingAggregate{}
	}
	sorted := append([]int64(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	var total int64
	for _, s := range sorted {
		total += s
	}
	avg := int64(math.Round(float64(total) / float64(len(sorted))))
	return TimingAggregate{
		TotalMs: total,
		AvgMs:   avg,
		P50Ms:   percentile(sorted, 50),
		P95Ms:   percentile(sorted, 95),
		MaxMs:   sorted[len(sorted)-1],
		Count:   len(sorted),
	}
}

// TierTiming is the (smaller) shape embedded per-tier in summary.tiers —
// avgMs + p50Ms only, to match spec §7 example.
type TierTiming struct {
	AvgMs int64 `json:"avgMs"`
	P50Ms int64 `json:"p50Ms"`
}

// TierTimingOf reduces a set of samples to avg/p50 only.
func TierTimingOf(samples []int64) TierTiming {
	if len(samples) == 0 {
		return TierTiming{}
	}
	sorted := append([]int64(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	var total int64
	for _, s := range sorted {
		total += s
	}
	return TierTiming{
		AvgMs: int64(math.Round(float64(total) / float64(len(sorted)))),
		P50Ms: percentile(sorted, 50),
	}
}

// percentile on a pre-sorted ascending slice, nearest-rank method.
func percentile(sorted []int64, p int) int64 {
	if len(sorted) == 0 {
		return 0
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 100 {
		return sorted[len(sorted)-1]
	}
	rank := int(math.Ceil(float64(p)/100.0*float64(len(sorted)))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}
