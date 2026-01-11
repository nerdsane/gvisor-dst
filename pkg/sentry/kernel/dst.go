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

package kernel

import (
	"gvisor.dev/gvisor/pkg/sync"
)

// DSTConfig holds configuration for Deterministic Simulation Testing mode.
type DSTConfig struct {
	// Enabled indicates whether DST mode is active.
	Enabled bool

	// Seed is the random seed for deterministic behavior.
	Seed uint64
}

// dstGlobalState holds global DST state.
var dstGlobalState struct {
	mu        sync.RWMutex
	config    DSTConfig
	scheduler *DSTScheduler
}

// EnableDSTScheduling enables deterministic task scheduling.
// This should be called during kernel initialization if DST mode is requested.
func EnableDSTScheduling(config DSTConfig) {
	dstGlobalState.mu.Lock()
	defer dstGlobalState.mu.Unlock()

	dstGlobalState.config = config

	if config.Enabled {
		if dstGlobalState.scheduler == nil {
			dstGlobalState.scheduler = NewDSTScheduler()
		}
		dstGlobalState.scheduler.Enable()
	}
}

// DisableDSTScheduling disables deterministic task scheduling.
func DisableDSTScheduling() {
	dstGlobalState.mu.Lock()
	defer dstGlobalState.mu.Unlock()

	dstGlobalState.config.Enabled = false
	if dstGlobalState.scheduler != nil {
		dstGlobalState.scheduler.Disable()
	}
}

// IsDSTSchedulingEnabled returns true if DST scheduling is enabled.
func IsDSTSchedulingEnabled() bool {
	dstGlobalState.mu.RLock()
	defer dstGlobalState.mu.RUnlock()
	return dstGlobalState.config.Enabled && dstGlobalState.scheduler != nil
}

// GetDSTScheduler returns the global DST scheduler, or nil if not enabled.
func GetDSTScheduler() *DSTScheduler {
	dstGlobalState.mu.RLock()
	defer dstGlobalState.mu.RUnlock()
	return dstGlobalState.scheduler
}

// GetDSTConfig returns the current DST configuration.
func GetDSTConfig() DSTConfig {
	dstGlobalState.mu.RLock()
	defer dstGlobalState.mu.RUnlock()
	return dstGlobalState.config
}

// DSTKernelState represents the complete DST state for the kernel.
// This combines scheduler state with any other DST-related kernel state.
type DSTKernelState struct {
	Config         DSTConfig
	SchedulerState DSTSchedulerState
}

// GetDSTKernelState returns the current DST kernel state for checkpointing.
func GetDSTKernelState() *DSTKernelState {
	dstGlobalState.mu.RLock()
	defer dstGlobalState.mu.RUnlock()

	if !dstGlobalState.config.Enabled || dstGlobalState.scheduler == nil {
		return nil
	}

	return &DSTKernelState{
		Config:         dstGlobalState.config,
		SchedulerState: dstGlobalState.scheduler.GetState(),
	}
}

// RestoreDSTKernelState restores DST kernel state from a checkpoint.
func RestoreDSTKernelState(state *DSTKernelState) {
	if state == nil {
		DisableDSTScheduling()
		return
	}

	dstGlobalState.mu.Lock()
	defer dstGlobalState.mu.Unlock()

	dstGlobalState.config = state.Config

	if state.Config.Enabled {
		if dstGlobalState.scheduler == nil {
			dstGlobalState.scheduler = NewDSTScheduler()
		}
		dstGlobalState.scheduler.SetState(state.SchedulerState)
		dstGlobalState.scheduler.Enable()
	}
}

// DSTTaskHooks provides hooks for integrating DST scheduling with tasks.
// These are called at key points in the task lifecycle.
type DSTTaskHooks struct{}

// OnTaskStart is called when a task starts running.
// It registers the task with the DST scheduler and waits for permission to run.
func (h *DSTTaskHooks) OnTaskStart(t *Task, tid ThreadID) {
	scheduler := GetDSTScheduler()
	if scheduler == nil {
		return
	}

	scheduler.RegisterTask(tid)
	scheduler.WaitForPermit(tid)
}

// OnTaskExit is called when a task exits.
// It unregisters the task from the DST scheduler.
func (h *DSTTaskHooks) OnTaskExit(t *Task, tid ThreadID) {
	scheduler := GetDSTScheduler()
	if scheduler == nil {
		return
	}

	scheduler.UnregisterTask(tid)
}

// OnSyscallEnter is called before a syscall is executed.
// This is a potential yield point for deterministic scheduling.
func (h *DSTTaskHooks) OnSyscallEnter(t *Task, tid ThreadID, sysno uintptr) {
	// Currently a no-op; yield happens after syscall completes.
}

// OnSyscallExit is called after a syscall completes.
// This is where we yield to allow other tasks to run.
func (h *DSTTaskHooks) OnSyscallExit(t *Task, tid ThreadID, sysno uintptr) {
	scheduler := GetDSTScheduler()
	if scheduler == nil {
		return
	}

	// Yield after each syscall for deterministic interleaving.
	scheduler.Yield(tid)
	scheduler.WaitForPermit(tid)
}

// OnTaskBlock is called when a task blocks (e.g., on I/O).
func (h *DSTTaskHooks) OnTaskBlock(t *Task, tid ThreadID) {
	scheduler := GetDSTScheduler()
	if scheduler == nil {
		return
	}

	scheduler.Block(tid)
}

// OnTaskUnblock is called when a task becomes ready after blocking.
func (h *DSTTaskHooks) OnTaskUnblock(t *Task, tid ThreadID) {
	scheduler := GetDSTScheduler()
	if scheduler == nil {
		return
	}

	scheduler.Unblock(tid)
	// Don't wait for permit here - let the scheduler decide when to run.
}

// DefaultDSTHooks is the default DST hooks instance.
var DefaultDSTHooks = &DSTTaskHooks{}

// DSTYieldPoint is called at explicit yield points in the code.
// This allows fine-grained control over task interleaving.
func DSTYieldPoint(t *Task, tid ThreadID, reason string) {
	scheduler := GetDSTScheduler()
	if scheduler == nil {
		return
	}

	scheduler.Yield(tid)
	scheduler.WaitForPermit(tid)
}
