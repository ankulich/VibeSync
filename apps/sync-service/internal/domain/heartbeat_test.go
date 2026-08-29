package domain

import (
	"math"
	"testing"
	"time"
)

func TestClientHeartbeatFirstHeartbeatEstablishesBaseline(t *testing.T) {
	t.Parallel()
	var hb ClientHeartbeat
	now := time.Now()
	// First heartbeat: no RTT/offset yet, just establishes baseline.
	hb.Update(1000, 1010, 1011, now)
	if hb.hasPrev != true {
		t.Error("first heartbeat should set hasPrev")
	}
	if hb.SmoothedRTT != 0 {
		t.Errorf("first heartbeat should have zero RTT; got %f", hb.SmoothedRTT)
	}
}

func TestClientHeartbeatSecondHeartbeatComputesRTT(t *testing.T) {
	t.Parallel()
	var hb ClientHeartbeat
	now := time.Now()
	// First heartbeat (baseline): t1=1000, t2=1010, t3=1011
	hb.Update(1000, 1010, 1011, now)
	// Second heartbeat: t1=2000 (= t4 of the first exchange)
	// RTT = (t4 - t1_prev) - (t3_prev - t2_prev) = (2000-1000) - (1011-1010) = 1000 - 1 = 999
	// offset = ((t2_prev - t1_prev) + (t3_prev - t4)) / 2 = ((1010-1000) + (1011-2000)) / 2 = (10 + (-989)) / 2 = -489.5
	hb.Update(2000, 2010, 2011, now.Add(time.Second))
	if hb.SmoothedRTT != 999 {
		t.Errorf("RTT = %f, want 999", hb.SmoothedRTT)
	}
	expectedOffset := -489.5
	if math.Abs(hb.SmoothedOffset-expectedOffset) > 0.01 {
		t.Errorf("Offset = %f, want %f", hb.SmoothedOffset, expectedOffset)
	}
}

func TestClientHeartbeatEWMASmoothing(t *testing.T) {
	t.Parallel()
	var hb ClientHeartbeat
	now := time.Now()
	// Heartbeat 1: baseline
	hb.Update(1000, 1010, 1011, now)
	// Heartbeat 2: RTT=999, offset=-489.5 → initialized (not EWMA'd)
	hb.Update(2000, 2010, 2011, now.Add(time.Second))
	firstRTT := hb.SmoothedRTT
	// Heartbeat 3: t1=3000, prev t1=2000, t2=2010, t3=2011
	// RTT = (3000-2000) - (2011-2010) = 1000 - 1 = 999
	// EWMA: 0.25*999 + 0.75*999 = 999 (same value → no change)
	hb.Update(3000, 3010, 3011, now.Add(2*time.Second))
	if hb.SmoothedRTT != firstRTT {
		t.Errorf("EWMA with same input should not change; got %f, want %f", hb.SmoothedRTT, firstRTT)
	}
}

func TestClientHeartbeatRTTNeverNegative(t *testing.T) {
	t.Parallel()
	var hb ClientHeartbeat
	now := time.Now()
	// First heartbeat.
	hb.Update(1000, 1010, 1011, now)
	// Second: t4 < t1_prev (clock went backwards) → RTT would be negative → clamped to 0.
	hb.Update(500, 1010, 1011, now.Add(time.Second))
	if hb.SmoothedRTT < 0 {
		t.Errorf("RTT should never be negative; got %f", hb.SmoothedRTT)
	}
}

func TestClientHeartbeatIsAlive(t *testing.T) {
	t.Parallel()
	var hb ClientHeartbeat
	now := time.Now()
	hb.Update(1000, 1010, 1011, now)
	if !hb.IsAlive(now, 5*time.Second) {
		t.Error("should be alive within 5s window")
	}
	if hb.IsAlive(now.Add(10*time.Second), 5*time.Second) {
		t.Error("should NOT be alive after 10s with 5s timeout")
	}
}

func TestClientHeartbeatReset(t *testing.T) {
	t.Parallel()
	var hb ClientHeartbeat
	now := time.Now()
	hb.Update(1000, 1010, 1011, now)
	hb.Update(2000, 2010, 2011, now.Add(time.Second))
	hb.SmoothedRTT = 500
	hb.Reset()
	if hb.hasPrev != false {
		t.Error("Reset should clear hasPrev")
	}
	if hb.SmoothedRTT != 0 {
		t.Error("Reset should clear SmoothedRTT")
	}
}

func TestDriftControllerSkipsWithFewPeers(t *testing.T) {
	t.Parallel()
	dc := NewDriftController()
	result := dc.Correct(100, time.Second, 1, 80)
	if result.CorrectionMs != 0 {
		t.Errorf("should skip with <2 peers; got correction %f", result.CorrectionMs)
	}
}

func TestDriftControllerSkipsWithLowConfidence(t *testing.T) {
	t.Parallel()
	dc := NewDriftController()
	result := dc.Correct(100, time.Second, 5, 20)
	if result.CorrectionMs != 0 {
		t.Errorf("should skip with confidence <30; got correction %f", result.CorrectionMs)
	}
}

func TestDriftControllerDiscontinuityResetsIntegral(t *testing.T) {
	t.Parallel()
	dc := NewDriftController()
	// Accumulate some integral.
	_ = dc.Correct(50, time.Second, 3, 80)
	_ = dc.Correct(50, time.Second, 3, 80)
	// Trigger discontinuity (> 2000ms).
	result := dc.Correct(3000, time.Second, 3, 80)
	if !result.ForceSnapshot {
		t.Error("should force snapshot on discontinuity")
	}
	if result.CorrectionMs != 0 {
		t.Errorf("discontinuity should produce zero correction; got %f", result.CorrectionMs)
	}
	// After discontinuity, integral was reset to 0. A subsequent small error
	// produces P + small I (the integral re-accumulates from zero on this cycle).
	result2 := dc.Correct(100, time.Second, 3, 80)
	// integral after this cycle = 100 * 1 = 100; correction = Kp*100 + Ki*100 = 17
	expected := dc.Kp*100 + dc.Ki*100
	if math.Abs(result2.CorrectionMs-expected) > 0.01 {
		t.Errorf("after reset, correction = %f, want %f", result2.CorrectionMs, expected)
	}
}

func TestDriftControllerProportionalOnly(t *testing.T) {
	t.Parallel()
	dc := NewDriftController()
	result := dc.Correct(100, time.Second, 3, 80)
	// First cycle: integral = 100 * 1 = 100, clamped within range.
	// Correction = Kp*100 + Ki*100 = 0.15*100 + 0.02*100 = 17
	expected := 0.15*100 + 0.02*100
	if math.Abs(result.CorrectionMs-expected) > 0.01 {
		t.Errorf("correction = %f, want %f", result.CorrectionMs, expected)
	}
}

func TestDriftControllerAntiWindup(t *testing.T) {
	t.Parallel()
	dc := NewDriftController()
	// Feed large positive error many times to saturate the integral.
	for i := 0; i < 100; i++ {
		_ = dc.Correct(500, time.Second, 3, 80)
	}
	// Integral should be clamped at ±200ms.
	if dc.integral > dc.IntegralClampMs+0.01 {
		t.Errorf("integral should be clamped at %f; got %f", dc.IntegralClampMs, dc.integral)
	}
}

func TestMedianFloatsEmpty(t *testing.T) {
	t.Parallel()
	if MedianFloats(nil) != 0 {
		t.Error("median of empty slice should be 0")
	}
}

func TestMedianFloatsOddCount(t *testing.T) {
	t.Parallel()
	vals := []float64{3, 1, 2}
	if m := MedianFloats(vals); m != 2 {
		t.Errorf("median of {3,1,2} = %f, want 2", m)
	}
}

func TestMedianFloatsEvenCount(t *testing.T) {
	t.Parallel()
	vals := []float64{1, 2, 3, 4}
	if m := MedianFloats(vals); m != 2.5 {
		t.Errorf("median of {1,2,3,4} = %f, want 2.5", m)
	}
}

func TestMaxAbsFloats(t *testing.T) {
	t.Parallel()
	cases := []struct {
		vals []float64
		want float64
	}{
		{nil, 0},
		{[]float64{5}, 5},
		{[]float64{-3, 7, -10, 2}, 10},
		{[]float64{-1, -2, -3}, 3},
	}
	for _, tc := range cases {
		if got := MaxAbsFloats(tc.vals); got != tc.want {
			t.Errorf("MaxAbsFloats(%v) = %f, want %f", tc.vals, got, tc.want)
		}
	}
}
