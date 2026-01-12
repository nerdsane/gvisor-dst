// Copyright 2026 The gVisor Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package time

import (
	"gvisor.dev/gvisor/pkg/errors/linuxerr"
	"gvisor.dev/gvisor/pkg/sync"
)

// VirtualClocks implements the Clocks interface with fully virtual time
// that only advances when explicitly requested. This enables deterministic
// simulation testing where the same sequence of operations always produces
// the same results.
//
// Key properties:
//   - Time does not advance automatically
//   - Time only advances via explicit Advance() calls
//   - Same seed + same operations = same time sequence
//   - Supports both monotonic and realtime clocks
//
// +stateify savable
type VirtualClocks struct {
	mu sync.RWMutex

	// monotonic is the current monotonic time in nanoseconds.
	// Monotonic time starts at 0 and only increases.
	monotonic int64

	// realtime is the current realtime (wall clock) in nanoseconds since Unix epoch.
	// Can be set to any value to simulate different wall clock times.
	realtime int64

	// frequency is the simulated CPU frequency in Hz.
	// Used for VDSO parameter generation.
	frequency uint64

	// baseCycles tracks a virtual TSC counter for VDSO compatibility.
	baseCycles TSCValue

	// listeners are notified when time advances.
	listeners []TimeListener

	// clockSkewNS is the accumulated clock skew in nanoseconds.
	// Applied to realtime on each GetTime() call to simulate clock drift.
	clockSkewNS int64

	// clockSkewRate is the rate of clock skew per nanosecond of time advance.
	// For example, 0.001 means the clock drifts 1ns per 1000ns of real time.
	clockSkewRate float64
}

// TimeListener is notified when virtual time advances.
type TimeListener interface {
	// OnTimeAdvance is called when virtual time advances.
	// The delta is the amount of time advanced in nanoseconds.
	OnTimeAdvance(delta int64)
}

// VirtualClocksConfig contains configuration for VirtualClocks.
type VirtualClocksConfig struct {
	// InitialRealtime is the initial realtime value in nanoseconds since Unix epoch.
	// Defaults to 0 (Unix epoch).
	InitialRealtime int64

	// InitialMonotonic is the initial monotonic time in nanoseconds.
	// Defaults to 0.
	InitialMonotonic int64

	// Frequency is the simulated CPU frequency in Hz.
	// Defaults to 1GHz.
	Frequency uint64
}

// DefaultVirtualClocksConfig returns a default configuration.
func DefaultVirtualClocksConfig() VirtualClocksConfig {
	return VirtualClocksConfig{
		InitialRealtime:  0,
		InitialMonotonic: 0,
		Frequency:        1_000_000_000, // 1 GHz
	}
}

// NewVirtualClocks creates a new VirtualClocks with the given configuration.
func NewVirtualClocks(cfg VirtualClocksConfig) *VirtualClocks {
	if cfg.Frequency == 0 {
		cfg.Frequency = 1_000_000_000 // 1 GHz default
	}
	// Initialize baseCycles to current TSC so VDSO time starts at InitialRealtime.
	// The VDSO calculates: time = BaseRef + (CurrentTSC - BaseCycles) / Frequency
	// By setting BaseCycles to the current TSC at initialization, time starts at BaseRef (0).
	initialCycles := Rdtsc()
	return &VirtualClocks{
		monotonic:  cfg.InitialMonotonic,
		realtime:   cfg.InitialRealtime,
		frequency:  cfg.Frequency,
		baseCycles: initialCycles,
	}
}

// Update implements Clocks.Update.
//
// For VirtualClocks, Update returns the current parameters without
// actually updating anything (since time only advances explicitly).
func (vc *VirtualClocks) Update() (monotonicParams Parameters, monotonicOk bool, realtimeParams Parameters, realtimeOk bool) {
	vc.mu.RLock()
	defer vc.mu.RUnlock()

	// Generate parameters for VDSO.
	// Since we control time, we use a simple linear model.
	monotonicParams = Parameters{
		BaseCycles: vc.baseCycles,
		BaseRef:    ReferenceNS(vc.monotonic),
		Frequency:  vc.frequency,
	}

	realtimeParams = Parameters{
		BaseCycles: vc.baseCycles,
		BaseRef:    ReferenceNS(vc.realtime),
		Frequency:  vc.frequency,
	}

	return monotonicParams, true, realtimeParams, true
}

// GetTime implements Clocks.GetTime.
// For Realtime, applies accumulated clock skew to simulate clock drift.
func (vc *VirtualClocks) GetTime(c ClockID) (int64, error) {
	vc.mu.RLock()
	defer vc.mu.RUnlock()

	switch c {
	case Monotonic:
		return vc.monotonic, nil
	case Realtime:
		// Apply clock skew to realtime
		return vc.realtime + vc.clockSkewNS, nil
	default:
		return 0, linuxerr.EINVAL
	}
}

// Advance advances both monotonic and realtime clocks by the given duration
// in nanoseconds. This is the primary way time progresses in deterministic mode.
//
// delta must be non-negative.
// If a clock skew rate is set, accumulated skew will increase proportionally.
func (vc *VirtualClocks) Advance(deltaNS int64) {
	if deltaNS < 0 {
		panic("VirtualClocks.Advance: negative delta")
	}
	if deltaNS == 0 {
		return
	}

	vc.mu.Lock()
	vc.monotonic += deltaNS
	vc.realtime += deltaNS
	// Apply clock skew rate if set
	if vc.clockSkewRate != 0 {
		skewDelta := int64(float64(deltaNS) * vc.clockSkewRate)
		vc.clockSkewNS += skewDelta
	}
	// Advance virtual TSC proportionally
	cycles := TSCValue((uint64(deltaNS) * vc.frequency) / 1_000_000_000)
	vc.baseCycles += cycles
	listeners := vc.listeners
	vc.mu.Unlock()

	// Notify listeners outside the lock
	for _, l := range listeners {
		l.OnTimeAdvance(deltaNS)
	}
}

// AdvanceMonotonic advances only the monotonic clock.
// This is useful for testing scenarios where realtime should not change.
func (vc *VirtualClocks) AdvanceMonotonic(deltaNS int64) {
	if deltaNS < 0 {
		panic("VirtualClocks.AdvanceMonotonic: negative delta")
	}
	if deltaNS == 0 {
		return
	}

	vc.mu.Lock()
	vc.monotonic += deltaNS
	cycles := TSCValue((uint64(deltaNS) * vc.frequency) / 1_000_000_000)
	vc.baseCycles += cycles
	vc.mu.Unlock()
}

// SetRealtime sets the realtime clock to a specific value.
// This can be used to simulate wall clock changes (e.g., NTP adjustments).
func (vc *VirtualClocks) SetRealtime(ns int64) {
	vc.mu.Lock()
	defer vc.mu.Unlock()
	vc.realtime = ns
}

// VirtualClocksState holds the complete clock state for checkpointing.
type VirtualClocksState struct {
	Monotonic     int64
	Realtime      int64
	BaseCycles    TSCValue
	ClockSkewNS   int64
	ClockSkewRate float64
}

// GetState returns the current state of both clocks for checkpointing.
func (vc *VirtualClocks) GetState() (monotonic, realtime int64, baseCycles TSCValue) {
	vc.mu.RLock()
	defer vc.mu.RUnlock()
	return vc.monotonic, vc.realtime, vc.baseCycles
}

// GetFullState returns the complete clock state including skew for checkpointing.
func (vc *VirtualClocks) GetFullState() VirtualClocksState {
	vc.mu.RLock()
	defer vc.mu.RUnlock()
	return VirtualClocksState{
		Monotonic:     vc.monotonic,
		Realtime:      vc.realtime,
		BaseCycles:    vc.baseCycles,
		ClockSkewNS:   vc.clockSkewNS,
		ClockSkewRate: vc.clockSkewRate,
	}
}

// SetState restores the clock state from a checkpoint.
func (vc *VirtualClocks) SetState(monotonic, realtime int64, baseCycles TSCValue) {
	vc.mu.Lock()
	defer vc.mu.Unlock()
	vc.monotonic = monotonic
	vc.realtime = realtime
	vc.baseCycles = baseCycles
}

// SetFullState restores the complete clock state including skew from a checkpoint.
func (vc *VirtualClocks) SetFullState(state VirtualClocksState) {
	vc.mu.Lock()
	defer vc.mu.Unlock()
	vc.monotonic = state.Monotonic
	vc.realtime = state.Realtime
	vc.baseCycles = state.BaseCycles
	vc.clockSkewNS = state.ClockSkewNS
	vc.clockSkewRate = state.ClockSkewRate
}

// AddListener adds a listener that will be notified when time advances.
func (vc *VirtualClocks) AddListener(l TimeListener) {
	vc.mu.Lock()
	defer vc.mu.Unlock()
	vc.listeners = append(vc.listeners, l)
}

// RemoveListener removes a previously added listener.
func (vc *VirtualClocks) RemoveListener(l TimeListener) {
	vc.mu.Lock()
	defer vc.mu.Unlock()
	for i, listener := range vc.listeners {
		if listener == l {
			vc.listeners = append(vc.listeners[:i], vc.listeners[i+1:]...)
			return
		}
	}
}

// InjectClockSkew injects a one-time clock skew.
// The skewNS is added to the accumulated skew and affects future GetTime(Realtime) calls.
// Positive values make the clock appear to run fast, negative values make it appear slow.
// This simulates gradual clock drift over time.
func (vc *VirtualClocks) InjectClockSkew(skewNS int64) {
	vc.mu.Lock()
	defer vc.mu.Unlock()
	vc.clockSkewNS += skewNS
}

// InjectClockJump injects a discrete clock jump.
// The jumpNS is immediately added to realtime, simulating NTP adjustments
// or other instantaneous clock corrections.
// Positive values jump forward, negative values jump backward.
func (vc *VirtualClocks) InjectClockJump(jumpNS int64) {
	vc.mu.Lock()
	defer vc.mu.Unlock()
	vc.realtime += jumpNS
}

// SetClockSkewRate sets the rate at which clock skew accumulates.
// The rate is applied during Advance() calls, causing gradual drift.
// A rate of 0.001 means 1ns of skew per 1000ns of time advance.
func (vc *VirtualClocks) SetClockSkewRate(rate float64) {
	vc.mu.Lock()
	defer vc.mu.Unlock()
	vc.clockSkewRate = rate
}

// GetClockSkew returns the current accumulated clock skew in nanoseconds.
func (vc *VirtualClocks) GetClockSkew() int64 {
	vc.mu.RLock()
	defer vc.mu.RUnlock()
	return vc.clockSkewNS
}

// ResetClockSkew resets the accumulated clock skew to zero.
func (vc *VirtualClocks) ResetClockSkew() {
	vc.mu.Lock()
	defer vc.mu.Unlock()
	vc.clockSkewNS = 0
	vc.clockSkewRate = 0
}

// Verify that VirtualClocks implements the Clocks interface.
var _ Clocks = (*VirtualClocks)(nil)
