package domain

import "time"

// DriftController implements the P+I (proportional-integral) drift correction
// described in docs/sync/algorithm.md. The controller nudges the authoritative
// media position toward the median client position, avoiding oscillation via
// gentle gains and anti-windup clamping.
//
// The controller runs at 1 Hz (not per-heartbeat) so corrections are smooth.
type DriftController struct {
	// Kp is the proportional gain. Gentle (0.15) to prevent oscillation.
	Kp float64
	// Ki is the integral gain. Slow (0.02) to clear residual steady-state
	// error over ~30 seconds.
	Ki float64
	// IntegralClampMs bounds the integral term to prevent windup (±200 ms).
	IntegralClampMs float64
	// DiscontinuityThresholdMs is the |error| above which the controller
	// treats the signal as a discontinuity (missed seek) rather than drift.
	// Triggers an integral reset + forces a full snapshot.
	DiscontinuityThresholdMs float64

	// integral is the accumulated error * dt. Clamped to ±IntegralClampMs.
	integral float64
}

// NewDriftController constructs a controller with the algorithm's default
// gains. Callers may override fields before use.
func NewDriftController() *DriftController {
	return &DriftController{
		Kp:                       0.15,
		Ki:                       0.02,
		IntegralClampMs:          200.0,
		DiscontinuityThresholdMs: 2000.0,
	}
}

// ControllerResult is the output of a correction cycle.
type ControllerResult struct {
	// CorrectionMs is the amount to subtract from state.media_time_ms.
	// Positive = clients are ahead → nudge authoritative position forward.
	CorrectionMs float64
	// ForceSnapshot is true when a discontinuity was detected; the caller
	// should reset the controller and force a full snapshot broadcast.
	ForceSnapshot bool
}

// Correct computes the drift correction for one controller cycle.
//
// Parameters:
//   - medianDriftMs: the room-wide median client drift. Positive = clients ahead.
//   - dt: time since the last correction cycle (typically 1s).
//   - activePeers: number of peers with recent heartbeats.
//   - confidence: 0..100 confidence in the current measurement.
//
// The controller skips correction when:
//   - activePeers < 2 (no consensus signal)
//   - confidence < 30 (unreliable measurement)
//   - |error| > DiscontinuityThresholdMs (treat as a missed seek → reset)
func (dc *DriftController) Correct(
	medianDriftMs float64,
	dt time.Duration,
	activePeers int,
	confidence uint32,
) ControllerResult {
	// Skip: not enough peers for a consensus signal.
	if activePeers < 2 {
		return ControllerResult{CorrectionMs: 0}
	}

	// Skip: signal is unreliable (clients just joined, measurements noisy).
	if confidence < 30 {
		return ControllerResult{CorrectionMs: 0}
	}

	// Discontinuity: the error is too large to be gradual drift; treat as a
	// missed seek. Reset the integral and force a full snapshot.
	if abs(medianDriftMs) > dc.DiscontinuityThresholdMs {
		dc.integral = 0
		return ControllerResult{CorrectionMs: 0, ForceSnapshot: true}
	}

	// Accumulate the integral (error * dt, in ms·seconds).
	dtSec := dt.Seconds()
	dc.integral += medianDriftMs * dtSec

	// Anti-windup: clamp the integral to ±IntegralClampMs.
	if dc.integral > dc.IntegralClampMs {
		dc.integral = dc.IntegralClampMs
	} else if dc.integral < -dc.IntegralClampMs {
		dc.integral = -dc.IntegralClampMs
	}

	// P+I correction. The correction is SUBTRACTED from media_time_ms
	// (if clients are ahead, error > 0, correction > 0 → we nudge forward).
	correction := dc.Kp*medianDriftMs + dc.Ki*dc.integral

	return ControllerResult{CorrectionMs: correction}
}

// Reset clears the integral term (e.g. after a host migration).
func (dc *DriftController) Reset() {
	dc.integral = 0
}
