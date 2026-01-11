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

package rand

import (
	"io"

	"gvisor.dev/gvisor/pkg/sync"
)

// dstState holds the DST mode state.
var dstState struct {
	mu                  sync.RWMutex
	enabled             bool
	deterministicReader *DeterministicReader
	originalReader      io.Reader
}

// EnableDST enables Deterministic Simulation Testing mode with the given seed.
// This replaces the global Reader with a DeterministicReader that produces
// reproducible random output.
//
// EnableDST should be called early in initialization, before any random
// numbers are generated.
func EnableDST(seed uint64) {
	dstState.mu.Lock()
	defer dstState.mu.Unlock()

	if dstState.enabled {
		// Already enabled, just reseed
		dstState.deterministicReader.Seed(seed)
		return
	}

	// Save original reader
	dstState.originalReader = Reader

	// Create and install deterministic reader
	dstState.deterministicReader = NewDeterministicReader(seed)
	Reader = dstState.deterministicReader
	dstState.enabled = true
}

// DisableDST disables Deterministic Simulation Testing mode and restores
// the original random reader.
func DisableDST() {
	dstState.mu.Lock()
	defer dstState.mu.Unlock()

	if !dstState.enabled {
		return
	}

	// Restore original reader
	Reader = dstState.originalReader
	dstState.enabled = false
	dstState.deterministicReader = nil
	dstState.originalReader = nil
}

// IsDSTEnabled returns true if DST mode is currently enabled.
func IsDSTEnabled() bool {
	dstState.mu.RLock()
	defer dstState.mu.RUnlock()
	return dstState.enabled
}

// GetDeterministicReader returns the DeterministicReader if DST mode is enabled,
// or nil otherwise. This can be used to access state management functions.
func GetDeterministicReader() *DeterministicReader {
	dstState.mu.RLock()
	defer dstState.mu.RUnlock()
	return dstState.deterministicReader
}

// DSTState represents the complete DST RNG state for checkpointing.
type DSTState struct {
	Enabled bool
	Seed    uint64
	State   [16]uint32
	Counter uint64
	Buffer  []byte
}

// GetDSTState returns the current DST state for checkpointing.
// Returns nil if DST is not enabled.
func GetDSTState() *DSTState {
	dstState.mu.RLock()
	defer dstState.mu.RUnlock()

	if !dstState.enabled {
		return nil
	}

	state, counter, buffer := dstState.deterministicReader.GetState()
	return &DSTState{
		Enabled: true,
		State:   state,
		Counter: counter,
		Buffer:  buffer,
	}
}

// RestoreDSTState restores DST state from a checkpoint.
// If state is nil or not enabled, DST is disabled.
func RestoreDSTState(s *DSTState) {
	if s == nil || !s.Enabled {
		DisableDST()
		return
	}

	dstState.mu.Lock()
	defer dstState.mu.Unlock()

	if !dstState.enabled {
		// Enable DST first
		dstState.originalReader = Reader
		dstState.deterministicReader = &DeterministicReader{}
		Reader = dstState.deterministicReader
		dstState.enabled = true
	}

	// Restore state
	dstState.deterministicReader.SetState(s.State, s.Counter, s.Buffer)
}

// Reseed reseeds the deterministic RNG with a new seed.
// This is useful for forking execution paths in simulation.
// Does nothing if DST is not enabled.
func Reseed(seed uint64) {
	dstState.mu.Lock()
	defer dstState.mu.Unlock()

	if dstState.enabled && dstState.deterministicReader != nil {
		dstState.deterministicReader.Seed(seed)
	}
}
