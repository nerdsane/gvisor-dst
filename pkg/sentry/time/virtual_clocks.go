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
	return &VirtualClocks{
		monotonic:  cfg.InitialMonotonic,
		realtime:   cfg.InitialRealtime,
		frequency:  cfg.Frequency,
		baseCycles: 0,
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
func (vc *VirtualClocks) GetTime(c ClockID) (int64, error) {
	vc.mu.RLock()
	defer vc.mu.RUnlock()

	switch c {
	case Monotonic:
		return vc.monotonic, nil
	case Realtime:
		return vc.realtime, nil
	default:
		return 0, linuxerr.EINVAL
	}
}

// Advance advances both monotonic and realtime clocks by the given duration
// in nanoseconds. This is the primary way time progresses in deterministic mode.
//
// delta must be non-negative.
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

// GetState returns the current state of both clocks for checkpointing.
func (vc *VirtualClocks) GetState() (monotonic, realtime int64, baseCycles TSCValue) {
	vc.mu.RLock()
	defer vc.mu.RUnlock()
	return vc.monotonic, vc.realtime, vc.baseCycles
}

// SetState restores the clock state from a checkpoint.
func (vc *VirtualClocks) SetState(monotonic, realtime int64, baseCycles TSCValue) {
	vc.mu.Lock()
	defer vc.mu.Unlock()
	vc.monotonic = monotonic
	vc.realtime = realtime
	vc.baseCycles = baseCycles
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

// Verify that VirtualClocks implements the Clocks interface.
var _ Clocks = (*VirtualClocks)(nil)
