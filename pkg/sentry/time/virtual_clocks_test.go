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
	"testing"
)

func TestVirtualClocksBasic(t *testing.T) {
	vc := NewVirtualClocks(DefaultVirtualClocksConfig())

	// Initial time should be 0
	mono, err := vc.GetTime(Monotonic)
	if err != nil {
		t.Fatalf("GetTime(Monotonic) failed: %v", err)
	}
	if mono != 0 {
		t.Errorf("Initial monotonic time = %d, want 0", mono)
	}

	real, err := vc.GetTime(Realtime)
	if err != nil {
		t.Fatalf("GetTime(Realtime) failed: %v", err)
	}
	if real != 0 {
		t.Errorf("Initial realtime = %d, want 0", real)
	}
}

func TestVirtualClocksAdvance(t *testing.T) {
	vc := NewVirtualClocks(DefaultVirtualClocksConfig())

	// Advance by 1 second (1e9 nanoseconds)
	vc.Advance(1_000_000_000)

	mono, _ := vc.GetTime(Monotonic)
	if mono != 1_000_000_000 {
		t.Errorf("After Advance(1e9): monotonic = %d, want 1e9", mono)
	}

	real, _ := vc.GetTime(Realtime)
	if real != 1_000_000_000 {
		t.Errorf("After Advance(1e9): realtime = %d, want 1e9", real)
	}

	// Advance again
	vc.Advance(500_000_000)

	mono, _ = vc.GetTime(Monotonic)
	if mono != 1_500_000_000 {
		t.Errorf("After second Advance: monotonic = %d, want 1.5e9", mono)
	}
}

func TestVirtualClocksAdvanceMonotonicOnly(t *testing.T) {
	cfg := DefaultVirtualClocksConfig()
	cfg.InitialRealtime = 1000
	vc := NewVirtualClocks(cfg)

	vc.AdvanceMonotonic(500)

	mono, _ := vc.GetTime(Monotonic)
	if mono != 500 {
		t.Errorf("monotonic = %d, want 500", mono)
	}

	real, _ := vc.GetTime(Realtime)
	if real != 1000 {
		t.Errorf("realtime = %d, want 1000 (unchanged)", real)
	}
}

func TestVirtualClocksSetRealtime(t *testing.T) {
	vc := NewVirtualClocks(DefaultVirtualClocksConfig())

	// Set realtime to a specific value
	unixEpoch2024 := int64(1704067200_000_000_000) // Jan 1, 2024 00:00:00 UTC
	vc.SetRealtime(unixEpoch2024)

	real, _ := vc.GetTime(Realtime)
	if real != unixEpoch2024 {
		t.Errorf("realtime = %d, want %d", real, unixEpoch2024)
	}

	// Monotonic should still be 0
	mono, _ := vc.GetTime(Monotonic)
	if mono != 0 {
		t.Errorf("monotonic = %d, want 0", mono)
	}
}

func TestVirtualClocksUpdate(t *testing.T) {
	cfg := DefaultVirtualClocksConfig()
	cfg.InitialMonotonic = 1000
	cfg.InitialRealtime = 2000
	vc := NewVirtualClocks(cfg)

	monoParams, monoOk, realParams, realOk := vc.Update()

	if !monoOk {
		t.Error("monotonic params not ok")
	}
	if !realOk {
		t.Error("realtime params not ok")
	}

	if monoParams.BaseRef != 1000 {
		t.Errorf("monotonic BaseRef = %d, want 1000", monoParams.BaseRef)
	}
	if realParams.BaseRef != 2000 {
		t.Errorf("realtime BaseRef = %d, want 2000", realParams.BaseRef)
	}
	if monoParams.Frequency != 1_000_000_000 {
		t.Errorf("Frequency = %d, want 1e9", monoParams.Frequency)
	}
}

func TestVirtualClocksCheckpoint(t *testing.T) {
	vc := NewVirtualClocks(DefaultVirtualClocksConfig())

	// Advance time
	vc.Advance(12345)
	vc.SetRealtime(67890)

	// Get state
	mono, real, cycles := vc.GetState()

	// Create new clocks and restore
	vc2 := NewVirtualClocks(DefaultVirtualClocksConfig())
	vc2.SetState(mono, real, cycles)

	mono2, _ := vc2.GetTime(Monotonic)
	real2, _ := vc2.GetTime(Realtime)

	if mono2 != 12345 {
		t.Errorf("Restored monotonic = %d, want 12345", mono2)
	}
	if real2 != 67890 {
		t.Errorf("Restored realtime = %d, want 67890", real2)
	}
}

func TestVirtualClocksDeterminism(t *testing.T) {
	// Run the same sequence twice and verify identical results
	run := func() (int64, int64) {
		vc := NewVirtualClocks(DefaultVirtualClocksConfig())
		vc.Advance(100)
		vc.Advance(200)
		vc.Advance(300)
		mono, _ := vc.GetTime(Monotonic)
		real, _ := vc.GetTime(Realtime)
		return mono, real
	}

	mono1, real1 := run()
	mono2, real2 := run()

	if mono1 != mono2 {
		t.Errorf("Non-deterministic monotonic: %d != %d", mono1, mono2)
	}
	if real1 != real2 {
		t.Errorf("Non-deterministic realtime: %d != %d", real1, real2)
	}
	if mono1 != 600 {
		t.Errorf("Expected monotonic = 600, got %d", mono1)
	}
}

type mockListener struct {
	advances []int64
}

func (m *mockListener) OnTimeAdvance(delta int64) {
	m.advances = append(m.advances, delta)
}

func TestVirtualClocksListener(t *testing.T) {
	vc := NewVirtualClocks(DefaultVirtualClocksConfig())

	listener := &mockListener{}
	vc.AddListener(listener)

	vc.Advance(100)
	vc.Advance(200)

	if len(listener.advances) != 2 {
		t.Fatalf("Expected 2 advances, got %d", len(listener.advances))
	}
	if listener.advances[0] != 100 {
		t.Errorf("First advance = %d, want 100", listener.advances[0])
	}
	if listener.advances[1] != 200 {
		t.Errorf("Second advance = %d, want 200", listener.advances[1])
	}

	// Remove listener
	vc.RemoveListener(listener)
	vc.Advance(300)

	if len(listener.advances) != 2 {
		t.Errorf("Listener still receiving after removal: %d advances", len(listener.advances))
	}
}

func TestVirtualClocksInvalidClockID(t *testing.T) {
	vc := NewVirtualClocks(DefaultVirtualClocksConfig())

	_, err := vc.GetTime(ClockID(999))
	if err == nil {
		t.Error("Expected error for invalid ClockID")
	}
}

func TestVirtualClocksZeroAdvance(t *testing.T) {
	vc := NewVirtualClocks(DefaultVirtualClocksConfig())

	listener := &mockListener{}
	vc.AddListener(listener)

	// Zero advance should be a no-op
	vc.Advance(0)

	if len(listener.advances) != 0 {
		t.Errorf("Zero advance triggered listener")
	}

	mono, _ := vc.GetTime(Monotonic)
	if mono != 0 {
		t.Errorf("Zero advance changed time to %d", mono)
	}
}
