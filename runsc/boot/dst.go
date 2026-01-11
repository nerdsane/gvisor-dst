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

package boot

import (
	"fmt"

	"gvisor.dev/gvisor/pkg/log"
	"gvisor.dev/gvisor/pkg/sentry/dst"
	"gvisor.dev/gvisor/pkg/sentry/time"
)

// DST RPC method names.
const (
	// DSTStep advances simulation by a number of steps.
	DSTStep = "DSTControl.Step"

	// DSTPause pauses the DST simulation.
	DSTPause = "DSTControl.Pause"

	// DSTResume resumes the DST simulation.
	DSTResume = "DSTControl.Resume"

	// DSTGetState returns the current DST state.
	DSTGetState = "DSTControl.GetState"

	// DSTSnapshot creates a DST snapshot.
	DSTSnapshot = "DSTControl.Snapshot"

	// DSTRestore restores to a DST snapshot.
	DSTRestore = "DSTControl.Restore"

	// DSTSetFaultProbabilities sets fault injection probabilities.
	DSTSetFaultProbabilities = "DSTControl.SetFaultProbabilities"

	// DSTScheduleFault schedules a fault for injection.
	DSTScheduleFault = "DSTControl.ScheduleFault"

	// DSTCancelFault cancels a scheduled fault.
	DSTCancelFault = "DSTControl.CancelFault"

	// DSTGetStats returns fault injection statistics.
	DSTGetStats = "DSTControl.GetStats"
)

// DSTControl provides DST (Deterministic Simulation Testing) control methods.
type DSTControl struct {
	// virtualClocks is the virtual clocks instance for direct time queries.
	virtualClocks *time.VirtualClocks
}

// NewDSTControl creates a new DST control handler.
func NewDSTControl(virtualClocks *time.VirtualClocks) *DSTControl {
	return &DSTControl{
		virtualClocks: virtualClocks,
	}
}

// StepArgs contains arguments for the Step RPC.
type StepArgs struct {
	// Steps is the number of simulation steps to advance.
	Steps uint64

	// DeltaNS is the time delta per step in nanoseconds.
	DeltaNS int64
}

// StepResult contains results from the Step RPC.
type StepResult struct {
	// StepCount is the total step count after stepping.
	StepCount uint64

	// VirtualTimeNS is the current virtual time in nanoseconds.
	VirtualTimeNS int64

	// Faults are the faults injected during stepping.
	Faults []dst.Fault

	// Running indicates if the simulation is still running.
	Running bool
}

// Step advances the simulation by a number of steps.
func (d *DSTControl) Step(args *StepArgs, result *StepResult) error {
	coord := dst.GetGlobalCoordinator()
	if coord == nil {
		return fmt.Errorf("DST coordinator not initialized")
	}

	if args.Steps == 0 {
		args.Steps = 1
	}
	if args.DeltaNS == 0 {
		args.DeltaNS = 1000000 // 1ms default
	}

	var allFaults []dst.Fault
	var running bool

	for i := uint64(0); i < args.Steps; i++ {
		faults, _, r := coord.Step(args.DeltaNS)
		allFaults = append(allFaults, faults...)
		running = r
		if !running {
			break
		}
	}

	result.StepCount = coord.GetStepCount()
	result.VirtualTimeNS = coord.GetCurrentTime()
	result.Faults = allFaults
	result.Running = running

	log.Debugf("DST.Step: steps=%d, time=%d, faults=%d", result.StepCount, result.VirtualTimeNS, len(result.Faults))
	return nil
}

// StateResult contains the current DST state.
type StateResult struct {
	// Running indicates if the simulation is running.
	Running bool

	// Seed is the random seed.
	Seed uint64

	// StepCount is the current step count.
	StepCount uint64

	// VirtualTimeNS is the current virtual time.
	VirtualTimeNS int64

	// FaultsInjected is the number of faults injected.
	FaultsInjected uint64

	// SnapshotCount is the number of snapshots.
	SnapshotCount int

	// MonotonicTimeNS is the current monotonic clock time.
	MonotonicTimeNS int64

	// RealtimeNS is the current realtime clock.
	RealtimeNS int64
}

// GetState returns the current DST state.
func (d *DSTControl) GetState(_ *struct{}, result *StateResult) error {
	coord := dst.GetGlobalCoordinator()
	if coord == nil {
		return fmt.Errorf("DST coordinator not initialized")
	}

	tree := coord.GetSnapshotTree()
	fi := coord.GetFaultInjector()
	coordState := coord.GetState()

	result.Running = coord.IsRunning()
	result.Seed = coordState.Config.Seed
	result.StepCount = coord.GetStepCount()
	result.VirtualTimeNS = coord.GetCurrentTime()
	result.FaultsInjected = fi.GetStats().FaultsInjected
	result.SnapshotCount = tree.SnapshotCount()

	// Get actual clock values if available
	if d.virtualClocks != nil {
		mono, rt, _ := d.virtualClocks.GetState()
		result.MonotonicTimeNS = mono
		result.RealtimeNS = rt
	}

	log.Debugf("DST.GetState: running=%v, seed=%d, step=%d, time=%d", result.Running, result.Seed, result.StepCount, result.VirtualTimeNS)
	return nil
}

// Pause pauses the DST simulation.
func (d *DSTControl) Pause(_ *struct{}, _ *struct{}) error {
	coord := dst.GetGlobalCoordinator()
	if coord == nil {
		return fmt.Errorf("DST coordinator not initialized")
	}

	coord.Stop("pause requested via RPC")
	log.Debugf("DST.Pause: simulation paused")
	return nil
}

// Resume resumes the DST simulation.
func (d *DSTControl) Resume(_ *struct{}, _ *struct{}) error {
	coord := dst.GetGlobalCoordinator()
	if coord == nil {
		return fmt.Errorf("DST coordinator not initialized")
	}

	coord.Start()
	log.Debugf("DST.Resume: simulation resumed")
	return nil
}

// SnapshotArgs contains arguments for snapshot creation.
type SnapshotArgs struct {
	// Description is an optional description for the snapshot.
	Description string
}

// SnapshotResult contains the result of snapshot creation.
type SnapshotResult struct {
	// ID is the snapshot ID.
	ID dst.SnapshotID
}

// Snapshot creates a DST snapshot.
func (d *DSTControl) Snapshot(args *SnapshotArgs, result *SnapshotResult) error {
	coord := dst.GetGlobalCoordinator()
	if coord == nil {
		return fmt.Errorf("DST coordinator not initialized")
	}

	tree := coord.GetSnapshotTree()
	state := &dst.DSTState{
		Metadata: dst.SnapshotMetadata{
			VirtualTimeNS: coord.GetCurrentTime(),
			StepCount:     coord.GetStepCount(),
			Description:   args.Description,
		},
	}

	id, err := tree.CreateSnapshot(state, nil)
	if err != nil {
		return fmt.Errorf("failed to create snapshot: %w", err)
	}

	result.ID = id
	log.Debugf("DST.Snapshot: created snapshot %v", id)
	return nil
}

// RestoreArgs contains arguments for snapshot restoration.
type RestoreArgs struct {
	// ID is the snapshot ID to restore to.
	ID dst.SnapshotID
}

// Restore restores to a DST snapshot.
func (d *DSTControl) Restore(args *RestoreArgs, _ *struct{}) error {
	coord := dst.GetGlobalCoordinator()
	if coord == nil {
		return fmt.Errorf("DST coordinator not initialized")
	}

	if err := coord.RestoreToSnapshot(args.ID); err != nil {
		return fmt.Errorf("failed to restore snapshot: %w", err)
	}

	log.Debugf("DST.Restore: restored to snapshot %v", args.ID)
	return nil
}

// SetFaultProbabilitiesArgs contains fault probability settings.
type SetFaultProbabilitiesArgs struct {
	dst.FaultProbabilities
}

// SetFaultProbabilities sets fault injection probabilities.
func (d *DSTControl) SetFaultProbabilities(args *SetFaultProbabilitiesArgs, _ *struct{}) error {
	coord := dst.GetGlobalCoordinator()
	if coord == nil {
		return fmt.Errorf("DST coordinator not initialized")
	}

	coord.GetFaultInjector().SetProbabilities(args.FaultProbabilities)
	log.Debugf("DST.SetFaultProbabilities: updated probabilities")
	return nil
}

// ScheduleFaultArgs contains arguments for scheduling a fault.
type ScheduleFaultArgs struct {
	// TriggerTimeNS is when the fault should be triggered.
	TriggerTimeNS int64

	// Fault is the fault to inject.
	Fault dst.Fault
}

// ScheduleFaultResult contains the result of scheduling a fault.
type ScheduleFaultResult struct {
	// FaultID is the ID of the scheduled fault.
	FaultID uint64
}

// ScheduleFault schedules a fault for injection.
func (d *DSTControl) ScheduleFault(args *ScheduleFaultArgs, result *ScheduleFaultResult) error {
	coord := dst.GetGlobalCoordinator()
	if coord == nil {
		return fmt.Errorf("DST coordinator not initialized")
	}

	result.FaultID = coord.GetFaultInjector().Schedule(args.TriggerTimeNS, args.Fault)
	log.Debugf("DST.ScheduleFault: scheduled fault %d at time %d", result.FaultID, args.TriggerTimeNS)
	return nil
}

// CancelFaultArgs contains arguments for canceling a scheduled fault.
type CancelFaultArgs struct {
	// FaultID is the ID of the fault to cancel.
	FaultID uint64
}

// CancelFault cancels a scheduled fault.
func (d *DSTControl) CancelFault(args *CancelFaultArgs, _ *struct{}) error {
	coord := dst.GetGlobalCoordinator()
	if coord == nil {
		return fmt.Errorf("DST coordinator not initialized")
	}

	if !coord.GetFaultInjector().CancelScheduled(args.FaultID) {
		return fmt.Errorf("fault %d not found", args.FaultID)
	}

	log.Debugf("DST.CancelFault: canceled fault %d", args.FaultID)
	return nil
}

// StatsResult contains fault injection statistics.
type StatsResult struct {
	dst.FaultStats
}

// GetStats returns fault injection statistics.
func (d *DSTControl) GetStats(_ *struct{}, result *StatsResult) error {
	coord := dst.GetGlobalCoordinator()
	if coord == nil {
		return fmt.Errorf("DST coordinator not initialized")
	}

	result.FaultStats = coord.GetFaultInjector().GetStats()
	log.Debugf("DST.GetStats: injected=%d, checks=%d", result.FaultsInjected, result.TotalChecks)
	return nil
}
