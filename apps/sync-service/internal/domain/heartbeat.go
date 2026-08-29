// Package domain contains the Sync Service's domain entities and the
// synchronization algorithm in code. See docs/sync/algorithm.md for the spec.
package domain

import (
	"math"
	"time"
)

// ewmaAlpha is the EWMA smoothing constant for RTT and offset (α = 0.25).
// Lower = smoother but slower to react; higher = more responsive but noisier.
// 0.25 is a standard choice for network RTT smoothing.
const ewmaAlpha = 0.25

// ClientHeartbeat tracks a single client's RTT, clock offset, and drift over
// time. Updated on every Heartbeat RPC via the four-timestamp NTP-style
// exchange described in docs/sync/algorithm.md.
type ClientHeartbeat struct {
	// SmoothedRTT is the EWMA-smoothed round-trip time in milliseconds.
	SmoothedRTT float64
	// SmoothedOffset is the EWMA-smoothed clock offset in milliseconds.
	// Positive means the client clock is BEHIND the server.
	SmoothedOffset float64
	// DriftMs is the last-computed media-position drift in milliseconds.
	// Positive means the client is AHEAD of the authoritative clock.
	DriftMs float64
	// LastSeen is the server time of the most recent heartbeat.
	LastSeen time.Time

	// prevT1 is the client_wall_time_ms from the previous HeartbeatRequest.
	// Stored so the next heartbeat can compute t4 (= the next request's t1)
	// and close the four-timestamp loop.
	prevT1 int64
	// prevT2 is the server receive time from the previous heartbeat.
	prevT2 int64
	// prevT3 is the server response time from the previous heartbeat.
	prevT3 int64
	// hasPrev is false until the first heartbeat establishes the baseline.
	hasPrev bool
}

// Update processes a heartbeat using the four-timestamp NTP-style exchange.
//
// The four timestamps are:
//
//	t1 = client_wall_time_ms at request send (from HeartbeatRequest)
//	t2 = server wall clock at request receive (measured by the server)
//	t3 = server wall clock at response send (measured by the server)
//	t4 = client_wall_time_ms at response receive (= the NEXT request's t1,
//	     which is the current request's client_wall_time_ms if we interpret
//	     the previous response's arrival as the current request's send)
//
// In practice, the exchange spans TWO heartbeats: the current heartbeat
// provides t1 (current send) and the previous heartbeat provides t2, t3.
// The t4 from the previous exchange is implicit in the current request's t1.
//
// RTT and offset are computed as:
//
//	RTT    = (t1_current - t1_prev) - (t3_prev - t2_prev)
//	offset = ((t2_prev - t1_prev) + (t3_prev - t1_current)) / 2
//
// For the FIRST heartbeat (no prior baseline), RTT/offset cannot be computed;
// the method stores the baseline and returns zeroed smoothed values.
func (c *ClientHeartbeat) Update(
	t1 int64, // client_wall_time_ms of the current request
	t2 int64, // server wall clock at request receive
	t3 int64, // server wall clock at response send
	now time.Time,
) {
	if !c.hasPrev {
		// First heartbeat: establish baseline, no RTT/offset yet.
		c.prevT1, c.prevT2, c.prevT3 = t1, t2, t3
		c.hasPrev = true
		c.LastSeen = now
		return
	}

	// Second+ heartbeat: compute RTT and offset using the prior exchange.
	// t4 = current t1 (the client's wall clock at the current request send,
	// which is when the client received the previous response + network delay).
	t4 := t1

	rtt := float64(t4-c.prevT1) - float64(c.prevT3-c.prevT2)
	if rtt < 0 {
		rtt = 0 // clock skew; clamp to non-negative
	}

	offset := (float64(c.prevT2-c.prevT1) + float64(c.prevT3-t4)) / 2.0

	// Apply EWMA smoothing.
	if c.SmoothedRTT == 0 {
		c.SmoothedRTT = rtt // initialize on first valid computation
		c.SmoothedOffset = offset
	} else {
		c.SmoothedRTT = ewmaAlpha*rtt + (1-ewmaAlpha)*c.SmoothedRTT
		c.SmoothedOffset = ewmaAlpha*offset + (1-ewmaAlpha)*c.SmoothedOffset
	}

	// Update baseline for the next exchange.
	c.prevT1, c.prevT2, c.prevT3 = t1, t2, t3
	c.LastSeen = now
}

// IsAlive reports whether the client's last heartbeat is within the liveness
// window. Used by host-migration detection.
func (c *ClientHeartbeat) IsAlive(now time.Time, timeout time.Duration) bool {
	return now.Sub(c.LastSeen) < timeout
}

// ClientMediaTimeAt translates a client-reported media time into server-time
// terms using the smoothed offset. This lets the server compare the client's
// position to the authoritative position without trusting the client clock.
func (c *ClientHeartbeat) ClientMediaTimeAt(clientMediaTimeMs int64) float64 {
	// Adjust the client's media time by the offset. If offset > 0 (client
	// behind), the client's media time at server-now is higher than reported.
	// We use float64 for sub-ms precision in the drift computation.
	return float64(clientMediaTimeMs) + c.SmoothedOffset
}

// Reset clears the heartbeat state (e.g. when a client reconnects after a
// long gap, the baseline is stale).
func (c *ClientHeartbeat) Reset() {
	c.SmoothedRTT = 0
	c.SmoothedOffset = 0
	c.DriftMs = 0
	c.prevT1, c.prevT2, c.prevT3 = 0, 0, 0
	c.hasPrev = false
}

// MedianFloats computes the median of a slice of float64s. Used by the
// room-wide P+I controller for drift and RTT aggregation.
func MedianFloats(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	// Copy to avoid mutating the caller's slice.
	sorted := make([]float64, len(vals))
	copy(sorted, vals)
	// Simple insertion sort (vals is small, typically <20 peers).
	for i := 1; i < len(sorted); i++ {
		key := sorted[i]
		j := i - 1
		for j >= 0 && sorted[j] > key {
			sorted[j+1] = sorted[j]
			j--
		}
		sorted[j+1] = key
	}
	mid := len(sorted) / 2
	if len(sorted)%2 == 0 {
		return (sorted[mid-1] + sorted[mid]) / 2
	}
	return sorted[mid]
}

// MaxAbsFloats returns the maximum absolute value across a slice. Used for
// drift_estimate_ms (the worst-case drift across peers).
func MaxAbsFloats(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	max := abs(vals[0])
	for _, v := range vals[1:] {
		a := abs(v)
		if a > max {
			max = a
		}
	}
	return max
}

// MedianRTT computes the median smoothed RTT across clients. Alias for
// MedianFloats; kept for readability at call sites.
func MedianRTT(rtts []float64) float64 {
	if len(rtts) == 0 {
		return 0
	}
	sorted := make([]float64, len(rtts))
	copy(sorted, rtts)
	for i := 1; i < len(sorted); i++ {
		key := sorted[i]
		j := i - 1
		for j >= 0 && sorted[j] > key {
			sorted[j+1] = sorted[j]
			j--
		}
		sorted[j+1] = key
	}
	mid := len(sorted) / 2
	if len(sorted)%2 == 0 {
		return (sorted[mid-1] + sorted[mid]) / 2
	}
	return sorted[mid]
}

// abs returns the absolute value of a float64.
func abs(x float64) float64 {
	return math.Abs(x)
}
