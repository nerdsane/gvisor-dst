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
	"encoding/json"
	"fmt"
	"sort"

	"gvisor.dev/gvisor/pkg/log"
	"gvisor.dev/gvisor/pkg/sentry/time"
	"gvisor.dev/gvisor/pkg/sync"
)

// FaultType represents the type of fault to inject.
type FaultType string

const (
	// Network faults
	FaultNetworkPartition      FaultType = "network_partition"
	FaultNetworkDrop           FaultType = "network_drop"
	FaultNetworkDelay          FaultType = "network_delay"
	FaultNetworkCorrupt        FaultType = "network_corrupt"
	FaultNetworkConnectRefused FaultType = "network_connect_refused"
	FaultNetworkConnectTimeout FaultType = "network_connect_timeout"
	FaultNetworkBindFailed     FaultType = "network_bind_failed"
	FaultNetworkListenFailed   FaultType = "network_listen_failed"
	FaultNetworkAcceptFailed   FaultType = "network_accept_failed"

	// Disk faults
	FaultDiskWriteFailure    FaultType = "disk_write_failure"
	FaultDiskReadFailure     FaultType = "disk_read_failure"
	FaultDiskReadCorruption  FaultType = "disk_read_corruption"
	FaultDiskWriteCorruption FaultType = "disk_write_corruption"
	FaultDiskSlow            FaultType = "disk_slow"

	// Process faults
	FaultProcessCrash  FaultType = "process_crash"
	FaultProcessPause  FaultType = "process_pause"
	FaultProcessOOM    FaultType = "process_oom"
	FaultProcessSIGKILL FaultType = "process_sigkill"

	// Memory faults
	FaultMemoryCorruption FaultType = "memory_corruption"
	FaultMemoryPressure   FaultType = "memory_pressure"

	// Time faults
	FaultClockSkew  FaultType = "clock_skew"
	FaultClockJump  FaultType = "clock_jump"
	FaultClockPause FaultType = "clock_pause"

	// Syscall faults
	FaultSyscallEINTR  FaultType = "syscall_eintr"
	FaultSyscallEIO    FaultType = "syscall_eio"
	FaultSyscallENOMEM FaultType = "syscall_enomem"
	FaultSyscallEAGAIN FaultType = "syscall_eagain"
)

// Fault represents a fault to be injected.
type Fault struct {
	// Type is the fault type.
	Type FaultType

	// Target is the target of the fault (e.g., task ID, file path, endpoint).
	Target string

	// Probability is the probability of the fault occurring (0.0-1.0).
	Probability float64

	// Duration is how long the fault lasts (nanoseconds), 0 for instant.
	Duration int64

	// Parameters holds fault-specific parameters.
	Parameters map[string]interface{}
}

// ScheduledFault represents a fault scheduled for a specific time.
type ScheduledFault struct {
	// TriggerTimeNS is when the fault should be triggered.
	TriggerTimeNS int64

	// Fault is the fault to inject.
	Fault Fault

	// ID is a unique identifier for this scheduled fault.
	ID uint64
}

// FaultProbabilities holds probabilities for random fault injection.
type FaultProbabilities struct {
	NetworkDrop          float64
	NetworkDelay         float64
	NetworkPartition     float64
	DiskWriteFailure     float64
	DiskReadFailure      float64
	DiskCorruption       float64
	ProcessCrash         float64
	ProcessPause         float64
	MemoryPressure       float64
	SyscallFailure       float64
}

// FaultProbabilitiesCalm returns probabilities for calm mode (no faults).
func FaultProbabilitiesCalm() FaultProbabilities {
	return FaultProbabilities{}
}

// FaultProbabilitiesModerate returns probabilities for moderate fault injection.
func FaultProbabilitiesModerate() FaultProbabilities {
	return FaultProbabilities{
		NetworkDrop:      0.01,
		NetworkDelay:     0.05,
		NetworkPartition: 0.001,
		DiskWriteFailure: 0.001,
		DiskReadFailure:  0.0005,
		DiskCorruption:   0.0001,
		ProcessCrash:     0.0001,
		ProcessPause:     0.0005,
		MemoryPressure:   0.001,
		SyscallFailure:   0.001,
	}
}

// FaultProbabilitiesChaos returns probabilities for chaos mode.
func FaultProbabilitiesChaos() FaultProbabilities {
	return FaultProbabilities{
		NetworkDrop:      0.05,
		NetworkDelay:     0.10,
		NetworkPartition: 0.01,
		DiskWriteFailure: 0.01,
		DiskReadFailure:  0.005,
		DiskCorruption:   0.001,
		ProcessCrash:     0.001,
		ProcessPause:     0.005,
		MemoryPressure:   0.01,
		SyscallFailure:   0.01,
	}
}

// FaultStats tracks fault injection statistics.
type FaultStats struct {
	TotalChecks     uint64
	FaultsInjected  uint64
	FaultsByType    map[FaultType]uint64
	FaultsByTarget  map[string]uint64
}

// FaultInjector manages fault injection for DST.
type FaultInjector struct {
	mu sync.RWMutex

	// +checklocks:mu
	enabled bool

	// +checklocks:mu
	seed uint64

	// +checklocks:mu
	probabilities FaultProbabilities

	// +checklocks:mu
	scheduled []ScheduledFault

	// +checklocks:mu
	nextFaultID uint64

	// +checklocks:mu
	activePartitions map[string]int64 // target -> end time

	// +checklocks:mu
	stats FaultStats

	// +checklocks:mu
	rngState uint64
}

// NewFaultInjector creates a new fault injector.
func NewFaultInjector(seed uint64) *FaultInjector {
	// Xorshift RNG cannot have state 0, so use a default non-zero value.
	rngState := seed
	if rngState == 0 {
		rngState = 0x853c49e6748fea9b // Arbitrary non-zero value
	}
	return &FaultInjector{
		enabled:          true,
		seed:             seed,
		probabilities:    FaultProbabilitiesCalm(),
		scheduled:        make([]ScheduledFault, 0),
		nextFaultID:      1,
		activePartitions: make(map[string]int64),
		stats: FaultStats{
			FaultsByType:   make(map[FaultType]uint64),
			FaultsByTarget: make(map[string]uint64),
		},
		rngState: rngState,
	}
}

// Enable enables or disables fault injection.
func (f *FaultInjector) Enable(enabled bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enabled = enabled
}

// IsEnabled returns whether fault injection is enabled.
func (f *FaultInjector) IsEnabled() bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.enabled
}

// SetSeed sets the random seed.
func (f *FaultInjector) SetSeed(seed uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seed = seed
	// Xorshift RNG cannot have state 0, so use a default non-zero value.
	if seed == 0 {
		f.rngState = 0x853c49e6748fea9b
	} else {
		f.rngState = seed
	}
}

// SetProbabilities sets the fault probabilities.
// Automatically enables fault injection if any probability is non-zero.
func (f *FaultInjector) SetProbabilities(probs FaultProbabilities) {
	f.mu.Lock()
	defer f.mu.Unlock()
	log.Warningf("DST SetProbabilities: DiskWrite=%.4f DiskRead=%.4f NetworkDrop=%.4f Syscall=%.4f",
		probs.DiskWriteFailure, probs.DiskReadFailure, probs.NetworkDrop, probs.SyscallFailure)
	f.probabilities = probs
	// Enable fault injection if any probability is non-zero.
	f.enabled = probs.NetworkDrop > 0 || probs.NetworkDelay > 0 ||
		probs.NetworkPartition > 0 || probs.DiskWriteFailure > 0 ||
		probs.DiskReadFailure > 0 || probs.DiskCorruption > 0 ||
		probs.ProcessCrash > 0 || probs.ProcessPause > 0 ||
		probs.MemoryPressure > 0 || probs.SyscallFailure > 0
}

// GetProbabilities returns the current fault probabilities.
func (f *FaultInjector) GetProbabilities() FaultProbabilities {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.probabilities
}

// Schedule schedules a fault for future injection.
func (f *FaultInjector) Schedule(triggerTime int64, fault Fault) uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()

	id := f.nextFaultID
	f.nextFaultID++

	sf := ScheduledFault{
		TriggerTimeNS: triggerTime,
		Fault:         fault,
		ID:            id,
	}

	f.scheduled = append(f.scheduled, sf)

	// Keep sorted by trigger time
	sort.Slice(f.scheduled, func(i, j int) bool {
		return f.scheduled[i].TriggerTimeNS < f.scheduled[j].TriggerTimeNS
	})

	return id
}

// CancelScheduled cancels a scheduled fault.
func (f *FaultInjector) CancelScheduled(id uint64) bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	for i, sf := range f.scheduled {
		if sf.ID == id {
			f.scheduled = append(f.scheduled[:i], f.scheduled[i+1:]...)
			return true
		}
	}
	return false
}

// ClearScheduled clears all scheduled faults.
func (f *FaultInjector) ClearScheduled() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scheduled = f.scheduled[:0]
}

// CheckInjection checks what faults should be injected at the given time.
func (f *FaultInjector) CheckInjection(timeNS int64) []Fault {
	f.mu.Lock()
	defer f.mu.Unlock()

	if !f.enabled {
		return nil
	}

	f.stats.TotalChecks++

	var faults []Fault

	// Check scheduled faults
	var remaining []ScheduledFault
	for _, sf := range f.scheduled {
		if sf.TriggerTimeNS <= timeNS {
			faults = append(faults, sf.Fault)
			f.recordFault(sf.Fault)

			// Track partitions
			if sf.Fault.Type == FaultNetworkPartition && sf.Fault.Duration > 0 {
				f.activePartitions[sf.Fault.Target] = timeNS + sf.Fault.Duration
			}
		} else {
			remaining = append(remaining, sf)
		}
	}
	f.scheduled = remaining

	// Check for partition heals
	for target, endTime := range f.activePartitions {
		if timeNS >= endTime {
			faults = append(faults, Fault{
				Type:   FaultNetworkPartition,
				Target: target,
				Parameters: map[string]interface{}{
					"action": "heal",
				},
			})
			delete(f.activePartitions, target)
		}
	}

	return faults
}

// NextScheduledTime returns the time of the next scheduled fault.
func (f *FaultInjector) NextScheduledTime(afterTimeNS int64) (int64, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	for _, sf := range f.scheduled {
		if sf.TriggerTimeNS > afterTimeNS {
			return sf.TriggerTimeNS, true
		}
	}
	return 0, false
}

// ShouldInjectFault checks if a fault should be injected based on probability.
// Uses deterministic RNG. Records the fault in stats if injected.
func (f *FaultInjector) ShouldInjectFault(faultType FaultType, target string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	if !f.enabled {
		return false
	}

	// Increment total checks for probability-based faults.
	f.stats.TotalChecks++

	// Log BEFORE the switch
	if f.stats.TotalChecks <= 3 {
		log.Warningf("DST ShouldInjectFault ENTRY: faultType=%v enabled=%v TotalChecks=%d", faultType, f.enabled, f.stats.TotalChecks)
	}

	var prob float64
	switch faultType {
	case FaultNetworkDrop:
		prob = f.probabilities.NetworkDrop
	case FaultNetworkDelay:
		prob = f.probabilities.NetworkDelay
	case FaultNetworkPartition:
		prob = f.probabilities.NetworkPartition
	case FaultDiskWriteFailure:
		prob = f.probabilities.DiskWriteFailure
	case FaultDiskReadFailure:
		prob = f.probabilities.DiskReadFailure
		log.Warningf("DST ShouldInjectFault: disk_read_failure prob=%.4f target=%s", prob, target)
	case FaultDiskReadCorruption, FaultDiskWriteCorruption:
		prob = f.probabilities.DiskCorruption
	case FaultProcessCrash:
		prob = f.probabilities.ProcessCrash
	case FaultProcessPause:
		prob = f.probabilities.ProcessPause
	case FaultMemoryPressure:
		prob = f.probabilities.MemoryPressure
	case FaultSyscallEINTR, FaultSyscallEIO, FaultSyscallENOMEM, FaultSyscallEAGAIN:
		prob = f.probabilities.SyscallFailure
	default:
		return false
	}

	// Debug: Always log for first few checks
	if f.stats.TotalChecks <= 5 {
		log.Warningf("DST FaultInjector CHECK: faultType=%s prob=%.4f enabled=%v checks=%d", faultType, prob, f.enabled, f.stats.TotalChecks)
	}

	if prob <= 0 {
		return false
	}

	// Simple deterministic RNG (xorshift)
	f.rngState ^= f.rngState << 13
	f.rngState ^= f.rngState >> 7
	f.rngState ^= f.rngState << 17

	// Convert to 0.0-1.0 range
	r := float64(f.rngState) / float64(^uint64(0))

	shouldFault := r < prob

	// Debug: Log every 100th check to avoid log spam
	if f.stats.TotalChecks%100 == 1 {
		log.Debugf("DST FaultInjector: type=%s prob=%.4f r=%.4f rngState=%d shouldFault=%v", faultType, prob, r, f.rngState, shouldFault)
	}

	if shouldFault {
		// Record the fault in stats.
		f.stats.FaultsInjected++
		f.stats.FaultsByType[faultType]++
		if target != "" {
			f.stats.FaultsByTarget[target]++
		}
	}

	return shouldFault
}

// recordFault records a fault injection in statistics.
func (f *FaultInjector) recordFault(fault Fault) {
	f.stats.FaultsInjected++
	f.stats.FaultsByType[fault.Type]++
	if fault.Target != "" {
		f.stats.FaultsByTarget[fault.Target]++
	}
}

// GetStats returns fault injection statistics.
func (f *FaultInjector) GetStats() FaultStats {
	f.mu.RLock()
	defer f.mu.RUnlock()

	stats := FaultStats{
		TotalChecks:    f.stats.TotalChecks,
		FaultsInjected: f.stats.FaultsInjected,
		FaultsByType:   make(map[FaultType]uint64),
		FaultsByTarget: make(map[string]uint64),
	}
	for k, v := range f.stats.FaultsByType {
		stats.FaultsByType[k] = v
	}
	for k, v := range f.stats.FaultsByTarget {
		stats.FaultsByTarget[k] = v
	}
	return stats
}

// ResetStats resets fault injection statistics.
func (f *FaultInjector) ResetStats() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stats = FaultStats{
		FaultsByType:   make(map[FaultType]uint64),
		FaultsByTarget: make(map[string]uint64),
	}
}

// FaultInjectorState represents checkpoint state.
type FaultInjectorState struct {
	Enabled          bool
	Seed             uint64
	RNGState         uint64
	Probabilities    FaultProbabilities
	Scheduled        []ScheduledFault
	NextFaultID      uint64
	ActivePartitions map[string]int64
	Stats            FaultStats
}

// GetState returns the current state for checkpointing.
func (f *FaultInjector) GetState() FaultInjectorState {
	f.mu.RLock()
	defer f.mu.RUnlock()

	scheduled := make([]ScheduledFault, len(f.scheduled))
	copy(scheduled, f.scheduled)

	activePartitions := make(map[string]int64)
	for k, v := range f.activePartitions {
		activePartitions[k] = v
	}

	faultsByType := make(map[FaultType]uint64)
	for k, v := range f.stats.FaultsByType {
		faultsByType[k] = v
	}

	faultsByTarget := make(map[string]uint64)
	for k, v := range f.stats.FaultsByTarget {
		faultsByTarget[k] = v
	}

	return FaultInjectorState{
		Enabled:          f.enabled,
		Seed:             f.seed,
		RNGState:         f.rngState,
		Probabilities:    f.probabilities,
		Scheduled:        scheduled,
		NextFaultID:      f.nextFaultID,
		ActivePartitions: activePartitions,
		Stats: FaultStats{
			TotalChecks:    f.stats.TotalChecks,
			FaultsInjected: f.stats.FaultsInjected,
			FaultsByType:   faultsByType,
			FaultsByTarget: faultsByTarget,
		},
	}
}

// SetState restores state from a checkpoint.
func (f *FaultInjector) SetState(state FaultInjectorState) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.enabled = state.Enabled
	f.seed = state.Seed
	f.rngState = state.RNGState
	f.probabilities = state.Probabilities
	f.scheduled = state.Scheduled
	f.nextFaultID = state.NextFaultID
	f.activePartitions = state.ActivePartitions
	f.stats = state.Stats
}

// PropertyKind represents the kind of property.
type PropertyKind string

const (
	PropertySafety    PropertyKind = "safety"    // Something bad never happens
	PropertyLiveness  PropertyKind = "liveness"  // Something good eventually happens
	PropertyInvariant PropertyKind = "invariant" // Always true
)

// Property represents a property to check.
type Property struct {
	// Name is the unique property name.
	Name string

	// Description describes what the property checks.
	Description string

	// Kind is the property kind.
	Kind PropertyKind

	// Check is the check function.
	Check PropertyCheck
}

// PropertyCheck represents a check to perform.
type PropertyCheck interface {
	// Check performs the check and returns the result.
	Check(state *SystemState) CheckResult
}

// CheckResult represents the result of a property check.
type CheckResult struct {
	// Status is the check status.
	Status CheckStatus

	// Reason explains the result.
	Reason string

	// Evidence provides additional context.
	Evidence map[string]interface{}
}

// CheckStatus represents the status of a check.
type CheckStatus string

const (
	CheckPass          CheckStatus = "pass"
	CheckFail          CheckStatus = "fail"
	CheckUnknown       CheckStatus = "unknown"
	CheckNotApplicable CheckStatus = "not_applicable"
)

// IsPass returns true if the check passed.
func (r CheckResult) IsPass() bool {
	return r.Status == CheckPass
}

// IsFail returns true if the check failed.
func (r CheckResult) IsFail() bool {
	return r.Status == CheckFail
}

// SystemState represents the system state for property checking.
type SystemState struct {
	// Values holds key-value state.
	Values map[string]string

	// History holds operation history.
	History []OperationRecord

	// DSTState holds DST-specific state.
	DSTState *DSTState

	// Custom holds application-specific state.
	Custom map[string]interface{}
}

// OperationRecord represents an operation in history.
type OperationRecord struct {
	Type      string
	Args      []string
	StartNS   int64
	EndNS     int64
	Result    string
	ProcessID string
}

// EqualsCheck checks if a value equals expected.
type EqualsCheck struct {
	Key      string
	Expected string
}

func (c *EqualsCheck) Check(state *SystemState) CheckResult {
	if state == nil || state.Values == nil {
		return CheckResult{Status: CheckUnknown, Reason: "no state"}
	}
	value, ok := state.Values[c.Key]
	if !ok {
		return CheckResult{Status: CheckUnknown, Reason: fmt.Sprintf("key %s not found", c.Key)}
	}
	if value == c.Expected {
		return CheckResult{Status: CheckPass}
	}
	return CheckResult{
		Status: CheckFail,
		Reason: fmt.Sprintf("%s = %s (expected %s)", c.Key, value, c.Expected),
	}
}

// RangeCheck checks if a numeric value is in range.
type RangeCheck struct {
	Key string
	Min int64
	Max int64
}

func (c *RangeCheck) Check(state *SystemState) CheckResult {
	if state == nil || state.Values == nil {
		return CheckResult{Status: CheckUnknown, Reason: "no state"}
	}
	valueStr, ok := state.Values[c.Key]
	if !ok {
		return CheckResult{Status: CheckUnknown, Reason: fmt.Sprintf("key %s not found", c.Key)}
	}
	var value int64
	if _, err := fmt.Sscanf(valueStr, "%d", &value); err != nil {
		return CheckResult{Status: CheckUnknown, Reason: fmt.Sprintf("key %s not numeric", c.Key)}
	}
	if value >= c.Min && value <= c.Max {
		return CheckResult{Status: CheckPass}
	}
	return CheckResult{
		Status: CheckFail,
		Reason: fmt.Sprintf("%s = %d (expected %d..%d)", c.Key, value, c.Min, c.Max),
	}
}

// CustomCheck allows custom check functions.
type CustomCheck struct {
	Name    string
	CheckFn func(state *SystemState) CheckResult
}

func (c *CustomCheck) Check(state *SystemState) CheckResult {
	if c.CheckFn == nil {
		return CheckResult{Status: CheckUnknown, Reason: "no check function"}
	}
	return c.CheckFn(state)
}

// PropertyChecker manages property checking.
type PropertyChecker struct {
	mu sync.RWMutex

	// +checklocks:mu
	properties []Property

	// +checklocks:mu
	results map[string][]CheckResult

	// +checklocks:mu
	enabled bool
}

// NewPropertyChecker creates a new property checker.
func NewPropertyChecker() *PropertyChecker {
	return &PropertyChecker{
		properties: make([]Property, 0),
		results:    make(map[string][]CheckResult),
		enabled:    true,
	}
}

// Enable enables or disables property checking.
func (p *PropertyChecker) Enable(enabled bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.enabled = enabled
}

// AddProperty adds a property to check.
func (p *PropertyChecker) AddProperty(prop Property) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.properties = append(p.properties, prop)
	p.results[prop.Name] = make([]CheckResult, 0)
}

// CheckAll checks all properties against the current state.
func (p *PropertyChecker) CheckAll(state *SystemState) map[string]CheckResult {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.enabled {
		return nil
	}

	results := make(map[string]CheckResult)
	for _, prop := range p.properties {
		result := prop.Check.Check(state)
		results[prop.Name] = result
		p.results[prop.Name] = append(p.results[prop.Name], result)
	}
	return results
}

// CheckProperty checks a single property.
func (p *PropertyChecker) CheckProperty(name string, state *SystemState) (CheckResult, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, prop := range p.properties {
		if prop.Name == name {
			result := prop.Check.Check(state)
			p.results[prop.Name] = append(p.results[prop.Name], result)
			return result, true
		}
	}
	return CheckResult{}, false
}

// GetResults returns all results for a property.
func (p *PropertyChecker) GetResults(name string) []CheckResult {
	p.mu.RLock()
	defer p.mu.RUnlock()

	results := p.results[name]
	out := make([]CheckResult, len(results))
	copy(out, results)
	return out
}

// GetFailures returns all failures across all properties.
func (p *PropertyChecker) GetFailures() map[string][]CheckResult {
	p.mu.RLock()
	defer p.mu.RUnlock()

	failures := make(map[string][]CheckResult)
	for name, results := range p.results {
		for _, r := range results {
			if r.IsFail() {
				failures[name] = append(failures[name], r)
			}
		}
	}
	return failures
}

// ClearResults clears all results.
func (p *PropertyChecker) ClearResults() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for name := range p.results {
		p.results[name] = make([]CheckResult, 0)
	}
}

// SimulationConfig holds configuration for simulation.
type SimulationConfig struct {
	// Seed is the random seed.
	Seed uint64

	// MaxSteps is the maximum number of simulation steps.
	MaxSteps uint64

	// MaxTimeNS is the maximum virtual time.
	MaxTimeNS int64

	// FaultProbabilities configures fault injection.
	FaultProbabilities FaultProbabilities

	// CheckPropertiesEveryN checks properties every N steps.
	CheckPropertiesEveryN uint64

	// SnapshotEveryN takes snapshots every N steps.
	SnapshotEveryN uint64

	// StopOnPropertyFailure stops simulation on property failure.
	StopOnPropertyFailure bool

	// MaxSnapshots limits the number of snapshots.
	MaxSnapshots int
}

// DefaultSimulationConfig returns default simulation configuration.
func DefaultSimulationConfig() SimulationConfig {
	return SimulationConfig{
		Seed:                  12345,
		MaxSteps:              1000000,
		MaxTimeNS:             60 * 1000000000, // 60 seconds
		FaultProbabilities:    FaultProbabilitiesModerate(),
		CheckPropertiesEveryN: 100,
		SnapshotEveryN:        1000,
		StopOnPropertyFailure: true,
		MaxSnapshots:          10000,
	}
}

// SimulationCoordinator coordinates DST simulation.
type SimulationCoordinator struct {
	mu sync.RWMutex

	// +checklocks:mu
	config SimulationConfig

	// +checklocks:mu
	faultInjector *FaultInjector

	// +checklocks:mu
	propertyChecker *PropertyChecker

	// +checklocks:mu
	snapshotTree *SnapshotTree

	// +checklocks:mu
	currentTimeNS int64

	// +checklocks:mu
	stepCount uint64

	// +checklocks:mu
	running bool

	// +checklocks:mu
	listeners []SimulationListener

	// virtualClocks is the virtual clocks instance for time advancement.
	// Set via SetVirtualClocks when DST mode is enabled.
	// +checklocks:mu
	virtualClocks *time.VirtualClocks
}

// SimulationListener is notified of simulation events.
type SimulationListener interface {
	OnSimulationStart(config SimulationConfig)
	OnSimulationStep(step uint64, timeNS int64)
	OnFaultInjected(fault Fault)
	OnPropertyChecked(name string, result CheckResult)
	OnSnapshotCreated(id SnapshotID)
	OnSimulationEnd(reason string)
}

// NewSimulationCoordinator creates a new simulation coordinator.
func NewSimulationCoordinator(config SimulationConfig) *SimulationCoordinator {
	return &SimulationCoordinator{
		config:          config,
		faultInjector:   NewFaultInjector(config.Seed),
		propertyChecker: NewPropertyChecker(),
		snapshotTree:    NewSnapshotTree(config.MaxSnapshots),
		listeners:       make([]SimulationListener, 0),
	}
}

// GetFaultInjector returns the fault injector.
func (c *SimulationCoordinator) GetFaultInjector() *FaultInjector {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.faultInjector
}

// GetPropertyChecker returns the property checker.
func (c *SimulationCoordinator) GetPropertyChecker() *PropertyChecker {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.propertyChecker
}

// GetSnapshotTree returns the snapshot tree.
func (c *SimulationCoordinator) GetSnapshotTree() *SnapshotTree {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snapshotTree
}

// AddListener adds a simulation listener.
func (c *SimulationCoordinator) AddListener(l SimulationListener) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.listeners = append(c.listeners, l)
}

// SetVirtualClocks sets the virtual clocks for time advancement.
// When set, the Step method will advance virtual time via the clocks.
func (c *SimulationCoordinator) SetVirtualClocks(vc *time.VirtualClocks) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.virtualClocks = vc
}

// GetVirtualClocks returns the virtual clocks instance, if set.
func (c *SimulationCoordinator) GetVirtualClocks() *time.VirtualClocks {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.virtualClocks
}

// Start starts the simulation.
func (c *SimulationCoordinator) Start() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.running = true
	c.faultInjector.SetProbabilities(c.config.FaultProbabilities)

	for _, l := range c.listeners {
		l.OnSimulationStart(c.config)
	}
}

// Stop stops the simulation.
func (c *SimulationCoordinator) Stop(reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.running = false

	for _, l := range c.listeners {
		l.OnSimulationEnd(reason)
	}
}

// IsRunning returns whether the simulation is running.
func (c *SimulationCoordinator) IsRunning() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.running
}

// Step performs one simulation step.
func (c *SimulationCoordinator) Step(deltaTimeNS int64) ([]Fault, map[string]CheckResult, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.running {
		return nil, nil, false
	}

	c.stepCount++
	c.currentTimeNS += deltaTimeNS

	// Advance virtual clocks if connected.
	if c.virtualClocks != nil {
		c.virtualClocks.Advance(deltaTimeNS)
	}

	// Check for faults to inject
	faults := c.faultInjector.CheckInjection(c.currentTimeNS)
	for _, fault := range faults {
		for _, l := range c.listeners {
			l.OnFaultInjected(fault)
		}
	}

	// Notify step
	for _, l := range c.listeners {
		l.OnSimulationStep(c.stepCount, c.currentTimeNS)
	}

	// Check properties periodically
	var propertyResults map[string]CheckResult
	if c.config.CheckPropertiesEveryN > 0 && c.stepCount%c.config.CheckPropertiesEveryN == 0 {
		state := c.captureSystemState()
		propertyResults = c.propertyChecker.CheckAll(state)
		for name, result := range propertyResults {
			for _, l := range c.listeners {
				l.OnPropertyChecked(name, result)
			}
			if result.IsFail() && c.config.StopOnPropertyFailure {
				c.running = false
				for _, l := range c.listeners {
					l.OnSimulationEnd(fmt.Sprintf("property %s failed: %s", name, result.Reason))
				}
				return faults, propertyResults, false
			}
		}
	}

	// Take snapshot periodically
	if c.config.SnapshotEveryN > 0 && c.stepCount%c.config.SnapshotEveryN == 0 {
		state := c.captureDSTState()
		id, _ := c.snapshotTree.CreateSnapshot(state, nil)
		for _, l := range c.listeners {
			l.OnSnapshotCreated(id)
		}
	}

	// Check termination conditions
	if c.stepCount >= c.config.MaxSteps {
		c.running = false
		for _, l := range c.listeners {
			l.OnSimulationEnd("max steps reached")
		}
		return faults, propertyResults, false
	}

	if c.currentTimeNS >= c.config.MaxTimeNS {
		c.running = false
		for _, l := range c.listeners {
			l.OnSimulationEnd("max time reached")
		}
		return faults, propertyResults, false
	}

	return faults, propertyResults, true
}

// GetCurrentTime returns the current virtual time.
func (c *SimulationCoordinator) GetCurrentTime() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.currentTimeNS
}

// GetStepCount returns the current step count.
func (c *SimulationCoordinator) GetStepCount() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.stepCount
}

// RestoreToSnapshot restores simulation to a snapshot.
func (c *SimulationCoordinator) RestoreToSnapshot(id SnapshotID) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	state, err := c.snapshotTree.RestoreSnapshot(id)
	if err != nil {
		return err
	}

	c.restoreDSTState(state)
	return nil
}

// captureSystemState captures current system state for property checking.
func (c *SimulationCoordinator) captureSystemState() *SystemState {
	return &SystemState{
		Values:   make(map[string]string),
		History:  make([]OperationRecord, 0),
		DSTState: c.captureDSTState(),
		Custom:   make(map[string]interface{}),
	}
}

// captureDSTState captures DST state for snapshotting.
func (c *SimulationCoordinator) captureDSTState() *DSTState {
	return &DSTState{
		Metadata: SnapshotMetadata{
			Seed:          c.config.Seed,
			VirtualTimeNS: c.currentTimeNS,
			StepCount:     c.stepCount,
			Description:   fmt.Sprintf("step-%d", c.stepCount),
		},
	}
}

// restoreDSTState restores DST state from a snapshot.
func (c *SimulationCoordinator) restoreDSTState(state *DSTState) {
	if state == nil {
		return
	}
	c.currentTimeNS = state.Metadata.VirtualTimeNS
	c.stepCount = state.Metadata.StepCount
}

// CoordinatorState represents checkpoint state for the coordinator.
type CoordinatorState struct {
	Config            SimulationConfig
	FaultInjectorState FaultInjectorState
	CurrentTimeNS     int64
	StepCount         uint64
	Running           bool
}

// GetState returns the coordinator state for checkpointing.
func (c *SimulationCoordinator) GetState() CoordinatorState {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return CoordinatorState{
		Config:            c.config,
		FaultInjectorState: c.faultInjector.GetState(),
		CurrentTimeNS:     c.currentTimeNS,
		StepCount:         c.stepCount,
		Running:           c.running,
	}
}

// SetState restores coordinator state from a checkpoint.
func (c *SimulationCoordinator) SetState(state CoordinatorState) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.config = state.Config
	c.faultInjector.SetState(state.FaultInjectorState)
	c.currentTimeNS = state.CurrentTimeNS
	c.stepCount = state.StepCount
	c.running = state.Running
}

// BloodhoundEvent represents an event for Bloodhound integration.
type BloodhoundEvent struct {
	Type      string                 `json:"type"`
	TimeNS    int64                  `json:"time_ns"`
	StepCount uint64                 `json:"step_count"`
	Data      map[string]interface{} `json:"data"`
}

// SerializeEvent serializes an event to JSON.
func SerializeEvent(event BloodhoundEvent) ([]byte, error) {
	return json.Marshal(event)
}

// DeserializeEvent deserializes an event from JSON.
func DeserializeEvent(data []byte) (BloodhoundEvent, error) {
	var event BloodhoundEvent
	err := json.Unmarshal(data, &event)
	return event, err
}

// BloodhoundEventType constants.
const (
	EventSimulationStart   = "simulation_start"
	EventSimulationEnd     = "simulation_end"
	EventSimulationStep    = "simulation_step"
	EventFaultInjected     = "fault_injected"
	EventFaultScheduled    = "fault_scheduled"
	EventPropertyChecked   = "property_checked"
	EventPropertyFailed    = "property_failed"
	EventSnapshotCreated   = "snapshot_created"
	EventSnapshotRestored  = "snapshot_restored"
)

// Global coordinator instance for convenience.
var globalCoordinator struct {
	mu          sync.RWMutex
	coordinator *SimulationCoordinator
}

// InitGlobalCoordinator initializes the global simulation coordinator.
func InitGlobalCoordinator(config SimulationConfig) {
	globalCoordinator.mu.Lock()
	defer globalCoordinator.mu.Unlock()
	globalCoordinator.coordinator = NewSimulationCoordinator(config)
}

// GetGlobalCoordinator returns the global simulation coordinator.
func GetGlobalCoordinator() *SimulationCoordinator {
	globalCoordinator.mu.RLock()
	defer globalCoordinator.mu.RUnlock()
	return globalCoordinator.coordinator
}

// GlobalFaultCheck is a convenience function to check if a fault should be injected.
func GlobalFaultCheck(faultType FaultType, target string) bool {
	coord := GetGlobalCoordinator()
	if coord == nil {
		return false
	}
	return coord.GetFaultInjector().ShouldInjectFault(faultType, target)
}

// GlobalScheduleFault is a convenience function to schedule a fault.
func GlobalScheduleFault(triggerTimeNS int64, fault Fault) uint64 {
	coord := GetGlobalCoordinator()
	if coord == nil {
		return 0
	}
	return coord.GetFaultInjector().Schedule(triggerTimeNS, fault)
}

// SyscallFaultResult represents the result of a syscall fault check.
type SyscallFaultResult struct {
	// ShouldFault indicates if a fault should be injected.
	ShouldFault bool
	// FaultType is the type of fault to inject.
	FaultType FaultType
}

// CheckReadFault checks if a read syscall should fail.
// Returns true if the read should fail with an error.
// fd is the file descriptor - faults are skipped for low-numbered FDs (0-9).
// This includes stdin/stdout/stderr and common system FDs like epoll, timerfd.
// Sockets are filtered at the syscall layer before calling this.
func CheckReadFault(fd int32, target string) SyscallFaultResult {
	// Skip fault injection on stdin/stdout/stderr (fd 0-2).
	// These are typically terminal I/O, not file I/O.
	// Programs that explicitly open files will use fd >= 3.
	if fd >= 0 && fd <= 2 {
		return SyscallFaultResult{}
	}
	coord := GetGlobalCoordinator()
	if coord == nil {
		return SyscallFaultResult{}
	}
	fi := coord.GetFaultInjector()
	if fi.ShouldInjectFault(FaultDiskReadFailure, target) {
		log.Warningf("DST: Injecting read fault on fd=%d target=%s", fd, target)
		return SyscallFaultResult{ShouldFault: true, FaultType: FaultDiskReadFailure}
	}
	return SyscallFaultResult{}
}

// CheckWriteFault checks if a write syscall should fail.
// Returns true if the write should fail with an error.
// fd is the file descriptor - faults are skipped for low-numbered FDs (0-9).
// This includes stdin/stdout/stderr and common system FDs like epoll, timerfd.
// Sockets are filtered at the syscall layer before calling this.
func CheckWriteFault(fd int32, target string) SyscallFaultResult {
	// Skip fault injection on stdin/stdout/stderr (fd 0-2).
	// These are typically terminal I/O, not file I/O.
	// Programs that explicitly open files will use fd >= 3.
	if fd >= 0 && fd <= 2 {
		return SyscallFaultResult{}
	}
	coord := GetGlobalCoordinator()
	if coord == nil {
		return SyscallFaultResult{}
	}
	fi := coord.GetFaultInjector()
	if fi.ShouldInjectFault(FaultDiskWriteFailure, target) {
		log.Warningf("DST: Injecting write fault on fd=%d target=%s", fd, target)
		return SyscallFaultResult{ShouldFault: true, FaultType: FaultDiskWriteFailure}
	}
	return SyscallFaultResult{}
}

// CheckNetworkSendFault checks if a network send should fail.
// Returns true if the send should be dropped or fail.
func CheckNetworkSendFault(target string) SyscallFaultResult {
	coord := GetGlobalCoordinator()
	if coord == nil {
		return SyscallFaultResult{}
	}
	fi := coord.GetFaultInjector()
	if fi.ShouldInjectFault(FaultNetworkDrop, target) {
		return SyscallFaultResult{ShouldFault: true, FaultType: FaultNetworkDrop}
	}
	return SyscallFaultResult{}
}

// CheckNetworkRecvFault checks if a network receive should fail.
// Returns true if the receive should fail.
func CheckNetworkRecvFault(target string) SyscallFaultResult {
	coord := GetGlobalCoordinator()
	if coord == nil {
		return SyscallFaultResult{}
	}
	fi := coord.GetFaultInjector()
	if fi.ShouldInjectFault(FaultNetworkDrop, target) {
		return SyscallFaultResult{ShouldFault: true, FaultType: FaultNetworkDrop}
	}
	return SyscallFaultResult{}
}

// CheckSyscallFault checks if a general syscall should fail with EINTR/EAGAIN.
// Returns true if the syscall should be interrupted.
func CheckSyscallFault(target string) SyscallFaultResult {
	coord := GetGlobalCoordinator()
	if coord == nil {
		return SyscallFaultResult{}
	}
	fi := coord.GetFaultInjector()
	if fi.ShouldInjectFault(FaultSyscallEINTR, target) {
		return SyscallFaultResult{ShouldFault: true, FaultType: FaultSyscallEINTR}
	}
	if fi.ShouldInjectFault(FaultSyscallEAGAIN, target) {
		return SyscallFaultResult{ShouldFault: true, FaultType: FaultSyscallEAGAIN}
	}
	return SyscallFaultResult{}
}

// CheckNetworkConnectFault checks if a connect() syscall should fail.
// Returns true if the connection should be refused or timeout.
func CheckNetworkConnectFault(target string) SyscallFaultResult {
	coord := GetGlobalCoordinator()
	if coord == nil {
		return SyscallFaultResult{}
	}
	fi := coord.GetFaultInjector()
	if fi.ShouldInjectFault(FaultNetworkConnectRefused, target) {
		log.Warningf("DST: Injecting connect refused fault target=%s", target)
		return SyscallFaultResult{ShouldFault: true, FaultType: FaultNetworkConnectRefused}
	}
	if fi.ShouldInjectFault(FaultNetworkConnectTimeout, target) {
		log.Warningf("DST: Injecting connect timeout fault target=%s", target)
		return SyscallFaultResult{ShouldFault: true, FaultType: FaultNetworkConnectTimeout}
	}
	// Also check general network drop for connect
	if fi.ShouldInjectFault(FaultNetworkDrop, target) {
		log.Warningf("DST: Injecting network drop on connect target=%s", target)
		return SyscallFaultResult{ShouldFault: true, FaultType: FaultNetworkDrop}
	}
	return SyscallFaultResult{}
}

// CheckNetworkBindFault checks if a bind() syscall should fail.
// Returns true if the bind should fail (e.g., address already in use).
func CheckNetworkBindFault(target string) SyscallFaultResult {
	coord := GetGlobalCoordinator()
	if coord == nil {
		return SyscallFaultResult{}
	}
	fi := coord.GetFaultInjector()
	if fi.ShouldInjectFault(FaultNetworkBindFailed, target) {
		log.Warningf("DST: Injecting bind fault target=%s", target)
		return SyscallFaultResult{ShouldFault: true, FaultType: FaultNetworkBindFailed}
	}
	return SyscallFaultResult{}
}

// CheckNetworkListenFault checks if a listen() syscall should fail.
// Returns true if listen should fail.
func CheckNetworkListenFault(target string) SyscallFaultResult {
	coord := GetGlobalCoordinator()
	if coord == nil {
		return SyscallFaultResult{}
	}
	fi := coord.GetFaultInjector()
	if fi.ShouldInjectFault(FaultNetworkListenFailed, target) {
		log.Warningf("DST: Injecting listen fault target=%s", target)
		return SyscallFaultResult{ShouldFault: true, FaultType: FaultNetworkListenFailed}
	}
	return SyscallFaultResult{}
}

// CheckNetworkAcceptFault checks if an accept() syscall should fail.
// Returns true if accept should fail.
func CheckNetworkAcceptFault(target string) SyscallFaultResult {
	coord := GetGlobalCoordinator()
	if coord == nil {
		return SyscallFaultResult{}
	}
	fi := coord.GetFaultInjector()
	if fi.ShouldInjectFault(FaultNetworkAcceptFailed, target) {
		log.Warningf("DST: Injecting accept fault target=%s", target)
		return SyscallFaultResult{ShouldFault: true, FaultType: FaultNetworkAcceptFailed}
	}
	// Also check general network drop
	if fi.ShouldInjectFault(FaultNetworkDrop, target) {
		log.Warningf("DST: Injecting network drop on accept target=%s", target)
		return SyscallFaultResult{ShouldFault: true, FaultType: FaultNetworkDrop}
	}
	return SyscallFaultResult{}
}
