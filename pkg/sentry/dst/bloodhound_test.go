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

package dst

import (
	"sync"
	"testing"
)

func TestFaultInjectorBasic(t *testing.T) {
	fi := NewFaultInjector(12345)

	if !fi.IsEnabled() {
		t.Error("Fault injector should be enabled by default")
	}

	fi.Enable(false)
	if fi.IsEnabled() {
		t.Error("Fault injector should be disabled")
	}

	fi.Enable(true)
	if !fi.IsEnabled() {
		t.Error("Fault injector should be enabled")
	}
}

func TestFaultInjectorSchedule(t *testing.T) {
	fi := NewFaultInjector(12345)

	// Schedule a fault
	id := fi.Schedule(1000000, Fault{
		Type:   FaultProcessCrash,
		Target: "node1",
	})

	if id == 0 {
		t.Error("Should return valid fault ID")
	}

	// Before trigger time - no faults
	faults := fi.CheckInjection(500000)
	if len(faults) != 0 {
		t.Errorf("Got %d faults before trigger time, want 0", len(faults))
	}

	// At trigger time - fault triggered
	faults = fi.CheckInjection(1000000)
	if len(faults) != 1 {
		t.Fatalf("Got %d faults at trigger time, want 1", len(faults))
	}
	if faults[0].Type != FaultProcessCrash {
		t.Errorf("Fault type = %s, want %s", faults[0].Type, FaultProcessCrash)
	}
	if faults[0].Target != "node1" {
		t.Errorf("Fault target = %s, want node1", faults[0].Target)
	}

	// After trigger time - fault removed
	faults = fi.CheckInjection(1500000)
	if len(faults) != 0 {
		t.Errorf("Got %d faults after trigger, want 0", len(faults))
	}
}

func TestFaultInjectorScheduleMultiple(t *testing.T) {
	fi := NewFaultInjector(12345)

	// Schedule multiple faults at different times
	fi.Schedule(1000, Fault{Type: FaultNetworkDrop, Target: "a"})
	fi.Schedule(3000, Fault{Type: FaultDiskWriteFailure, Target: "b"})
	fi.Schedule(2000, Fault{Type: FaultProcessPause, Target: "c"})

	// Check at time 2500 - should get first two
	faults := fi.CheckInjection(2500)
	if len(faults) != 2 {
		t.Errorf("Got %d faults at 2500, want 2", len(faults))
	}

	// Check at time 3500 - should get the last one
	faults = fi.CheckInjection(3500)
	if len(faults) != 1 {
		t.Errorf("Got %d faults at 3500, want 1", len(faults))
	}
}

func TestFaultInjectorCancel(t *testing.T) {
	fi := NewFaultInjector(12345)

	id := fi.Schedule(1000, Fault{Type: FaultProcessCrash, Target: "node1"})

	// Cancel the fault
	if !fi.CancelScheduled(id) {
		t.Error("Cancel should succeed")
	}

	// Fault should not trigger
	faults := fi.CheckInjection(1500)
	if len(faults) != 0 {
		t.Errorf("Got %d faults after cancel, want 0", len(faults))
	}

	// Cancel non-existent should return false
	if fi.CancelScheduled(99999) {
		t.Error("Cancel non-existent should fail")
	}
}

func TestFaultInjectorPartitionHeal(t *testing.T) {
	fi := NewFaultInjector(12345)

	// Schedule a partition with duration
	fi.Schedule(1000, Fault{
		Type:     FaultNetworkPartition,
		Target:   "partition1",
		Duration: 2000, // 2000ns duration
	})

	// Trigger partition
	faults := fi.CheckInjection(1000)
	if len(faults) != 1 {
		t.Fatalf("Got %d faults at partition time, want 1", len(faults))
	}

	// During partition - no faults
	faults = fi.CheckInjection(2000)
	if len(faults) != 0 {
		t.Errorf("Got %d faults during partition, want 0", len(faults))
	}

	// After duration - should heal
	faults = fi.CheckInjection(3500)
	if len(faults) != 1 {
		t.Fatalf("Got %d faults after partition, want 1 (heal)", len(faults))
	}
	if faults[0].Type != FaultNetworkPartition {
		t.Errorf("Heal fault type = %s, want %s", faults[0].Type, FaultNetworkPartition)
	}
}

func TestFaultInjectorProbability(t *testing.T) {
	fi := NewFaultInjector(12345)

	// With no probability, should never inject
	for i := 0; i < 100; i++ {
		if fi.ShouldInjectFault(FaultNetworkDrop, "target") {
			t.Error("Should not inject with 0 probability")
		}
	}

	// Set high probability
	fi.SetProbabilities(FaultProbabilities{
		NetworkDrop: 1.0, // Always inject
	})

	// Should always inject
	injected := 0
	for i := 0; i < 100; i++ {
		if fi.ShouldInjectFault(FaultNetworkDrop, "target") {
			injected++
		}
	}
	if injected != 100 {
		t.Errorf("Injected %d times with 100%% probability, want 100", injected)
	}
}

func TestFaultInjectorDeterminism(t *testing.T) {
	runSequence := func(seed uint64) []bool {
		fi := NewFaultInjector(seed)
		fi.SetProbabilities(FaultProbabilities{
			NetworkDrop: 0.5,
		})

		results := make([]bool, 100)
		for i := 0; i < 100; i++ {
			results[i] = fi.ShouldInjectFault(FaultNetworkDrop, "target")
		}
		return results
	}

	// Same seed should produce same results
	results1 := runSequence(12345)
	results2 := runSequence(12345)

	for i := range results1 {
		if results1[i] != results2[i] {
			t.Errorf("Determinism failed at index %d", i)
			break
		}
	}

	// Different seed should produce different results
	results3 := runSequence(54321)
	same := true
	for i := range results1 {
		if results1[i] != results3[i] {
			same = false
			break
		}
	}
	if same {
		t.Error("Different seeds should produce different results")
	}
}

func TestFaultInjectorStats(t *testing.T) {
	fi := NewFaultInjector(12345)

	fi.Schedule(1000, Fault{Type: FaultProcessCrash, Target: "node1"})
	fi.Schedule(2000, Fault{Type: FaultNetworkDrop, Target: "node2"})

	fi.CheckInjection(1500)
	fi.CheckInjection(2500)

	stats := fi.GetStats()
	if stats.TotalChecks != 2 {
		t.Errorf("TotalChecks = %d, want 2", stats.TotalChecks)
	}
	if stats.FaultsInjected != 2 {
		t.Errorf("FaultsInjected = %d, want 2", stats.FaultsInjected)
	}
	if stats.FaultsByType[FaultProcessCrash] != 1 {
		t.Error("Should have 1 ProcessCrash fault")
	}
	if stats.FaultsByType[FaultNetworkDrop] != 1 {
		t.Error("Should have 1 NetworkDrop fault")
	}
}

func TestFaultInjectorCheckpoint(t *testing.T) {
	fi := NewFaultInjector(12345)

	fi.Schedule(5000, Fault{Type: FaultProcessCrash, Target: "node1"})
	fi.CheckInjection(1000)

	// Save state
	state := fi.GetState()

	// Modify
	fi.Schedule(6000, Fault{Type: FaultNetworkDrop, Target: "node2"})
	fi.CheckInjection(5500)

	// Restore
	fi.SetState(state)

	// Should be back to original state
	stats := fi.GetStats()
	if stats.FaultsInjected != 0 {
		t.Errorf("After restore, FaultsInjected = %d, want 0", stats.FaultsInjected)
	}

	// Original scheduled fault should still be there
	faults := fi.CheckInjection(5500)
	if len(faults) != 1 {
		t.Errorf("After restore, got %d faults, want 1", len(faults))
	}
}

func TestPropertyCheckerEquals(t *testing.T) {
	checker := NewPropertyChecker()

	checker.AddProperty(Property{
		Name:        "status_ok",
		Description: "Status should be OK",
		Kind:        PropertySafety,
		Check: &EqualsCheck{
			Key:      "status",
			Expected: "ok",
		},
	})

	// Test pass
	state := &SystemState{
		Values: map[string]string{"status": "ok"},
	}
	results := checker.CheckAll(state)
	if !results["status_ok"].IsPass() {
		t.Error("Expected pass for matching status")
	}

	// Test fail
	state.Values["status"] = "error"
	results = checker.CheckAll(state)
	if !results["status_ok"].IsFail() {
		t.Error("Expected fail for non-matching status")
	}
}

func TestPropertyCheckerRange(t *testing.T) {
	checker := NewPropertyChecker()

	checker.AddProperty(Property{
		Name:        "count_valid",
		Description: "Count should be 0-100",
		Kind:        PropertyInvariant,
		Check: &RangeCheck{
			Key: "count",
			Min: 0,
			Max: 100,
		},
	})

	// Test pass
	state := &SystemState{
		Values: map[string]string{"count": "50"},
	}
	results := checker.CheckAll(state)
	if !results["count_valid"].IsPass() {
		t.Error("Expected pass for count in range")
	}

	// Test fail
	state.Values["count"] = "150"
	results = checker.CheckAll(state)
	if !results["count_valid"].IsFail() {
		t.Error("Expected fail for count out of range")
	}
}

func TestPropertyCheckerCustom(t *testing.T) {
	checker := NewPropertyChecker()

	checker.AddProperty(Property{
		Name:        "custom_check",
		Description: "Custom check",
		Kind:        PropertySafety,
		Check: &CustomCheck{
			Name: "custom",
			CheckFn: func(state *SystemState) CheckResult {
				if v, ok := state.Values["custom"]; ok && v == "valid" {
					return CheckResult{Status: CheckPass}
				}
				return CheckResult{Status: CheckFail, Reason: "custom check failed"}
			},
		},
	})

	state := &SystemState{
		Values: map[string]string{"custom": "valid"},
	}
	results := checker.CheckAll(state)
	if !results["custom_check"].IsPass() {
		t.Error("Expected pass for custom check")
	}

	state.Values["custom"] = "invalid"
	results = checker.CheckAll(state)
	if !results["custom_check"].IsFail() {
		t.Error("Expected fail for custom check")
	}
}

func TestPropertyCheckerFailures(t *testing.T) {
	checker := NewPropertyChecker()

	checker.AddProperty(Property{
		Name: "always_fail",
		Kind: PropertySafety,
		Check: &CustomCheck{
			CheckFn: func(state *SystemState) CheckResult {
				return CheckResult{Status: CheckFail, Reason: "always fails"}
			},
		},
	})

	checker.AddProperty(Property{
		Name: "always_pass",
		Kind: PropertySafety,
		Check: &CustomCheck{
			CheckFn: func(state *SystemState) CheckResult {
				return CheckResult{Status: CheckPass}
			},
		},
	})

	state := &SystemState{Values: make(map[string]string)}
	checker.CheckAll(state)
	checker.CheckAll(state)

	failures := checker.GetFailures()
	if len(failures["always_fail"]) != 2 {
		t.Errorf("Got %d failures for always_fail, want 2", len(failures["always_fail"]))
	}
	if len(failures["always_pass"]) != 0 {
		t.Error("Should have no failures for always_pass")
	}
}

func TestSimulationCoordinatorBasic(t *testing.T) {
	config := DefaultSimulationConfig()
	config.MaxSteps = 10
	config.CheckPropertiesEveryN = 0  // Disable property checking
	config.SnapshotEveryN = 0         // Disable snapshots

	coord := NewSimulationCoordinator(config)

	if coord.IsRunning() {
		t.Error("Should not be running initially")
	}

	coord.Start()
	if !coord.IsRunning() {
		t.Error("Should be running after Start")
	}

	// Run steps
	for i := 0; i < 10; i++ {
		_, _, running := coord.Step(1000000) // 1ms steps
		if i < 9 && !running {
			t.Errorf("Should still be running at step %d", i)
		}
	}

	if coord.IsRunning() {
		t.Error("Should stop after max steps")
	}
}

func TestSimulationCoordinatorFaultInjection(t *testing.T) {
	config := DefaultSimulationConfig()
	config.MaxSteps = 100
	config.CheckPropertiesEveryN = 0
	config.SnapshotEveryN = 0

	coord := NewSimulationCoordinator(config)

	// Schedule a fault
	fi := coord.GetFaultInjector()
	fi.Schedule(5000000, Fault{
		Type:   FaultProcessCrash,
		Target: "node1",
	})

	coord.Start()

	// Run until fault triggers
	var faults []Fault
	for i := 0; i < 10; i++ {
		f, _, _ := coord.Step(1000000)
		if len(f) > 0 {
			faults = f
			break
		}
	}

	if len(faults) != 1 {
		t.Errorf("Got %d faults, want 1", len(faults))
	}
}

func TestSimulationCoordinatorPropertyFailure(t *testing.T) {
	config := DefaultSimulationConfig()
	config.MaxSteps = 100
	config.CheckPropertiesEveryN = 1
	config.StopOnPropertyFailure = true
	config.SnapshotEveryN = 0

	coord := NewSimulationCoordinator(config)

	// Add a property that fails
	pc := coord.GetPropertyChecker()
	pc.AddProperty(Property{
		Name: "fails_at_5",
		Kind: PropertySafety,
		Check: &CustomCheck{
			CheckFn: func(state *SystemState) CheckResult {
				if state.DSTState != nil && state.DSTState.Metadata.StepCount >= 5 {
					return CheckResult{Status: CheckFail, Reason: "step >= 5"}
				}
				return CheckResult{Status: CheckPass}
			},
		},
	})

	coord.Start()

	// Run until stopped
	steps := 0
	for steps < 100 {
		_, _, running := coord.Step(1000000)
		steps++
		if !running {
			break
		}
	}

	if steps >= 10 {
		t.Errorf("Should have stopped early due to property failure, ran %d steps", steps)
	}
}

func TestSimulationCoordinatorSnapshots(t *testing.T) {
	config := DefaultSimulationConfig()
	config.MaxSteps = 50
	config.CheckPropertiesEveryN = 0
	config.SnapshotEveryN = 10

	coord := NewSimulationCoordinator(config)
	coord.Start()

	// Run all steps
	for coord.IsRunning() {
		coord.Step(1000000)
	}

	// Should have created snapshots
	tree := coord.GetSnapshotTree()
	if tree.SnapshotCount() < 4 {
		t.Errorf("Got %d snapshots, expected at least 4", tree.SnapshotCount())
	}
}

type testListener struct {
	mu             sync.Mutex
	started        bool
	ended          bool
	endReason      string
	steps          uint64
	faultsInjected int
	propertiesChecked int
	snapshotsCreated int
}

func (l *testListener) OnSimulationStart(config SimulationConfig) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.started = true
}

func (l *testListener) OnSimulationStep(step uint64, timeNS int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.steps = step
}

func (l *testListener) OnFaultInjected(fault Fault) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.faultsInjected++
}

func (l *testListener) OnPropertyChecked(name string, result CheckResult) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.propertiesChecked++
}

func (l *testListener) OnSnapshotCreated(id SnapshotID) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.snapshotsCreated++
}

func (l *testListener) OnSimulationEnd(reason string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ended = true
	l.endReason = reason
}

func TestSimulationCoordinatorListener(t *testing.T) {
	config := DefaultSimulationConfig()
	config.MaxSteps = 20
	config.CheckPropertiesEveryN = 5
	config.SnapshotEveryN = 10

	coord := NewSimulationCoordinator(config)

	listener := &testListener{}
	coord.AddListener(listener)

	// Add a property
	pc := coord.GetPropertyChecker()
	pc.AddProperty(Property{
		Name: "test",
		Kind: PropertySafety,
		Check: &CustomCheck{
			CheckFn: func(state *SystemState) CheckResult {
				return CheckResult{Status: CheckPass}
			},
		},
	})

	// Schedule a fault
	fi := coord.GetFaultInjector()
	fi.Schedule(5000000, Fault{Type: FaultProcessCrash, Target: "node1"})

	coord.Start()

	for coord.IsRunning() {
		coord.Step(1000000)
	}

	listener.mu.Lock()
	defer listener.mu.Unlock()

	if !listener.started {
		t.Error("Should have received start event")
	}
	if !listener.ended {
		t.Error("Should have received end event")
	}
	if listener.steps != 20 {
		t.Errorf("Steps = %d, want 20", listener.steps)
	}
	if listener.faultsInjected != 1 {
		t.Errorf("FaultsInjected = %d, want 1", listener.faultsInjected)
	}
	if listener.propertiesChecked < 3 {
		t.Errorf("PropertiesChecked = %d, want at least 3", listener.propertiesChecked)
	}
	if listener.snapshotsCreated < 1 {
		t.Errorf("SnapshotsCreated = %d, want at least 1", listener.snapshotsCreated)
	}
}

func TestSimulationCoordinatorRestore(t *testing.T) {
	config := DefaultSimulationConfig()
	config.MaxSteps = 100
	config.CheckPropertiesEveryN = 0
	config.SnapshotEveryN = 10

	coord := NewSimulationCoordinator(config)
	coord.Start()

	// Run 25 steps
	for i := 0; i < 25; i++ {
		coord.Step(1000000)
	}

	// Should have snapshots at steps 10 and 20
	tree := coord.GetSnapshotTree()

	// Get path to current
	path, _ := tree.GetPath(tree.GetCurrentID())
	if len(path) < 2 {
		t.Fatal("Should have at least 2 snapshots in path")
	}

	// Restore to first snapshot
	err := coord.RestoreToSnapshot(path[0])
	if err != nil {
		t.Fatalf("RestoreToSnapshot failed: %v", err)
	}

	// Step count should be reset
	stepCount := coord.GetStepCount()
	if stepCount >= 20 {
		t.Errorf("After restore, step count = %d, want < 20", stepCount)
	}
}

func TestBloodhoundEventSerialization(t *testing.T) {
	event := BloodhoundEvent{
		Type:      EventFaultInjected,
		TimeNS:    1000000,
		StepCount: 100,
		Data: map[string]interface{}{
			"fault_type": "process_crash",
			"target":     "node1",
		},
	}

	// Serialize
	data, err := SerializeEvent(event)
	if err != nil {
		t.Fatalf("SerializeEvent failed: %v", err)
	}

	// Deserialize
	restored, err := DeserializeEvent(data)
	if err != nil {
		t.Fatalf("DeserializeEvent failed: %v", err)
	}

	if restored.Type != event.Type {
		t.Errorf("Type = %s, want %s", restored.Type, event.Type)
	}
	if restored.TimeNS != event.TimeNS {
		t.Errorf("TimeNS = %d, want %d", restored.TimeNS, event.TimeNS)
	}
	if restored.Data["fault_type"] != "process_crash" {
		t.Error("Data not restored correctly")
	}
}

func TestGlobalCoordinator(t *testing.T) {
	config := DefaultSimulationConfig()
	InitGlobalCoordinator(config)

	coord := GetGlobalCoordinator()
	if coord == nil {
		t.Fatal("Global coordinator should be initialized")
	}

	// Schedule via global function
	id := GlobalScheduleFault(1000000, Fault{Type: FaultProcessCrash, Target: "node1"})
	if id == 0 {
		t.Error("GlobalScheduleFault should return valid ID")
	}
}

func BenchmarkFaultInjectorCheck(b *testing.B) {
	fi := NewFaultInjector(12345)
	fi.SetProbabilities(FaultProbabilitiesModerate())

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fi.ShouldInjectFault(FaultNetworkDrop, "target")
	}
}

func BenchmarkFaultInjectorScheduledCheck(b *testing.B) {
	fi := NewFaultInjector(12345)

	// Schedule many faults
	for i := 0; i < 1000; i++ {
		fi.Schedule(int64(i)*1000, Fault{Type: FaultProcessCrash, Target: "node"})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fi.CheckInjection(int64(i) * 100)
	}
}

func BenchmarkPropertyCheck(b *testing.B) {
	checker := NewPropertyChecker()

	for i := 0; i < 10; i++ {
		checker.AddProperty(Property{
			Name: "prop",
			Kind: PropertySafety,
			Check: &EqualsCheck{
				Key:      "status",
				Expected: "ok",
			},
		})
	}

	state := &SystemState{
		Values: map[string]string{"status": "ok"},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		checker.CheckAll(state)
	}
}

func BenchmarkSimulationStep(b *testing.B) {
	config := DefaultSimulationConfig()
	config.MaxSteps = uint64(b.N) + 1
	config.CheckPropertiesEveryN = 0
	config.SnapshotEveryN = 0

	coord := NewSimulationCoordinator(config)
	coord.Start()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		coord.Step(1000000)
	}
}
