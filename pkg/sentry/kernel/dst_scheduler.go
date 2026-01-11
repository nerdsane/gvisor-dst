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
	"sort"

	"gvisor.dev/gvisor/pkg/sync"
)

// DSTSchedulerState represents the complete scheduler state for checkpointing.
type DSTSchedulerState struct {
	// Enabled indicates whether DST scheduling is active.
	Enabled bool
	// CurrentTID is the TID of the currently running task (0 if none).
	CurrentTID ThreadID
	// YieldCounter tracks how many yields have occurred (for determinism verification).
	YieldCounter uint64
	// ReadyTIDs is the ordered list of ready task TIDs.
	ReadyTIDs []ThreadID
}

// DSTScheduler provides deterministic task scheduling for simulation testing.
// When enabled, it coordinates task execution so that only one task runs at a time,
// and the order of execution is deterministic (based on TID ordering).
//
// The scheduler uses a permit-based system:
// - Each task waits for a "permit" before executing
// - After completing a unit of work (e.g., a syscall), the task yields
// - The scheduler grants the permit to the next task deterministically
//
// This approach ensures that given the same initial state and inputs,
// the execution order is always identical.
type DSTScheduler struct {
	mu sync.Mutex

	// enabled indicates whether deterministic scheduling is active.
	enabled bool

	// tasks maps TID to task scheduling state.
	tasks map[ThreadID]*dstTaskState

	// readyQueue contains TIDs of tasks that are ready to run, sorted by TID.
	readyQueue []ThreadID

	// currentTask is the TID of the currently running task (0 if none).
	currentTask ThreadID

	// yieldCounter tracks the number of yields for debugging/verification.
	yieldCounter uint64

	// globalCond is used to wake up all waiting tasks when scheduling decisions change.
	globalCond *sync.Cond

	// listeners are notified of scheduling events.
	listeners []DSTSchedulerListener
}

// dstTaskState tracks the scheduling state of a single task.
type dstTaskState struct {
	// tid is the task's thread ID.
	tid ThreadID

	// ready indicates whether the task is ready to run.
	ready bool

	// blocked indicates whether the task is blocked (waiting on I/O, etc.).
	blocked bool

	// exited indicates whether the task has exited.
	exited bool

	// permitChan is used to grant the task permission to run.
	// The task blocks on this channel until it receives a permit.
	permitChan chan struct{}
}

// DSTSchedulerListener is notified of scheduling events.
type DSTSchedulerListener interface {
	// OnTaskScheduled is called when a task is granted the permit to run.
	OnTaskScheduled(tid ThreadID)
	// OnTaskYielded is called when a task yields.
	OnTaskYielded(tid ThreadID)
	// OnTaskBlocked is called when a task blocks.
	OnTaskBlocked(tid ThreadID)
	// OnTaskUnblocked is called when a task becomes ready again.
	OnTaskUnblocked(tid ThreadID)
}

// NewDSTScheduler creates a new deterministic scheduler.
func NewDSTScheduler() *DSTScheduler {
	s := &DSTScheduler{
		tasks:      make(map[ThreadID]*dstTaskState),
		readyQueue: make([]ThreadID, 0),
	}
	s.globalCond = sync.NewCond(&s.mu)
	return s
}

// Enable activates deterministic scheduling.
func (s *DSTScheduler) Enable() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enabled = true
}

// Disable deactivates deterministic scheduling.
func (s *DSTScheduler) Disable() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enabled = false
	// Wake up all tasks so they can proceed without deterministic scheduling.
	s.globalCond.Broadcast()
}

// IsEnabled returns whether deterministic scheduling is active.
func (s *DSTScheduler) IsEnabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enabled
}

// RegisterTask registers a new task with the scheduler.
// This must be called before the task starts running.
func (s *DSTScheduler) RegisterTask(tid ThreadID) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.tasks[tid]; exists {
		return // Already registered
	}

	state := &dstTaskState{
		tid:        tid,
		ready:      true,
		blocked:    false,
		exited:     false,
		permitChan: make(chan struct{}, 1),
	}
	s.tasks[tid] = state

	// Add to ready queue and sort by TID for deterministic ordering.
	s.readyQueue = append(s.readyQueue, tid)
	s.sortReadyQueue()

	// If this is the first task or no task is running, schedule it.
	if s.enabled && s.currentTask == 0 && len(s.readyQueue) > 0 {
		s.scheduleNextLocked()
	}
}

// UnregisterTask removes a task from the scheduler.
// This should be called when a task exits.
func (s *DSTScheduler) UnregisterTask(tid ThreadID) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, exists := s.tasks[tid]
	if !exists {
		return
	}

	state.exited = true
	delete(s.tasks, tid)

	// Remove from ready queue.
	s.removeFromReadyQueue(tid)

	// If the current task is exiting, schedule the next one.
	if s.currentTask == tid {
		s.currentTask = 0
		s.scheduleNextLocked()
	}
}

// WaitForPermit blocks until the task is granted permission to run.
// Returns immediately if DST scheduling is disabled.
func (s *DSTScheduler) WaitForPermit(tid ThreadID) {
	s.mu.Lock()
	if !s.enabled {
		s.mu.Unlock()
		return
	}

	state, exists := s.tasks[tid]
	if !exists {
		s.mu.Unlock()
		return
	}

	// If we already have the permit, proceed.
	if s.currentTask == tid {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	// Wait for permit.
	<-state.permitChan
}

// Yield gives up the current time slice and allows other tasks to run.
// The task will be re-added to the ready queue and may run again later.
func (s *DSTScheduler) Yield(tid ThreadID) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.enabled {
		return
	}

	state, exists := s.tasks[tid]
	if !exists {
		return
	}

	// Can only yield if we're the current task.
	if s.currentTask != tid {
		return
	}

	s.yieldCounter++

	// Notify listeners.
	for _, l := range s.listeners {
		l.OnTaskYielded(tid)
	}

	// Mark as ready and add back to queue.
	state.ready = true
	if !s.isInReadyQueue(tid) {
		s.readyQueue = append(s.readyQueue, tid)
		s.sortReadyQueue()
	}

	// Clear current task and schedule next.
	s.currentTask = 0
	s.scheduleNextLocked()
}

// Block marks a task as blocked (e.g., waiting on I/O).
// The task is removed from the ready queue until Unblock is called.
func (s *DSTScheduler) Block(tid ThreadID) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.enabled {
		return
	}

	state, exists := s.tasks[tid]
	if !exists {
		return
	}

	state.blocked = true
	state.ready = false

	// Notify listeners.
	for _, l := range s.listeners {
		l.OnTaskBlocked(tid)
	}

	// Remove from ready queue.
	s.removeFromReadyQueue(tid)

	// If this was the current task, schedule next.
	if s.currentTask == tid {
		s.currentTask = 0
		s.scheduleNextLocked()
	}
}

// Unblock marks a task as ready again after being blocked.
func (s *DSTScheduler) Unblock(tid ThreadID) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.enabled {
		return
	}

	state, exists := s.tasks[tid]
	if !exists {
		return
	}

	state.blocked = false
	state.ready = true

	// Notify listeners.
	for _, l := range s.listeners {
		l.OnTaskUnblocked(tid)
	}

	// Add to ready queue.
	if !s.isInReadyQueue(tid) {
		s.readyQueue = append(s.readyQueue, tid)
		s.sortReadyQueue()
	}

	// If no task is running, schedule this one.
	if s.currentTask == 0 {
		s.scheduleNextLocked()
	}
}

// GetState returns the current scheduler state for checkpointing.
func (s *DSTScheduler) GetState() DSTSchedulerState {
	s.mu.Lock()
	defer s.mu.Unlock()

	readyTIDs := make([]ThreadID, len(s.readyQueue))
	copy(readyTIDs, s.readyQueue)

	return DSTSchedulerState{
		Enabled:      s.enabled,
		CurrentTID:   s.currentTask,
		YieldCounter: s.yieldCounter,
		ReadyTIDs:    readyTIDs,
	}
}

// SetState restores the scheduler state from a checkpoint.
func (s *DSTScheduler) SetState(state DSTSchedulerState) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.enabled = state.Enabled
	s.currentTask = state.CurrentTID
	s.yieldCounter = state.YieldCounter
	s.readyQueue = make([]ThreadID, len(state.ReadyTIDs))
	copy(s.readyQueue, state.ReadyTIDs)

	// Re-grant permit to current task if any.
	if s.currentTask != 0 {
		if taskState, exists := s.tasks[s.currentTask]; exists {
			select {
			case taskState.permitChan <- struct{}{}:
			default:
			}
		}
	}
}

// AddListener adds a listener for scheduling events.
func (s *DSTScheduler) AddListener(l DSTSchedulerListener) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listeners = append(s.listeners, l)
}

// GetCurrentTask returns the TID of the currently running task.
func (s *DSTScheduler) GetCurrentTask() ThreadID {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentTask
}

// GetYieldCounter returns the total number of yields.
func (s *DSTScheduler) GetYieldCounter() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.yieldCounter
}

// scheduleNextLocked picks the next task to run and grants it the permit.
// Must be called with s.mu held.
func (s *DSTScheduler) scheduleNextLocked() {
	if !s.enabled || len(s.readyQueue) == 0 {
		return
	}

	// Pick the first task in the ready queue (lowest TID for determinism).
	nextTID := s.readyQueue[0]
	s.readyQueue = s.readyQueue[1:]

	s.currentTask = nextTID

	// Notify listeners.
	for _, l := range s.listeners {
		l.OnTaskScheduled(nextTID)
	}

	// Grant permit to the task.
	if state, exists := s.tasks[nextTID]; exists {
		select {
		case state.permitChan <- struct{}{}:
		default:
		}
	}
}

// sortReadyQueue sorts the ready queue by TID for deterministic ordering.
func (s *DSTScheduler) sortReadyQueue() {
	sort.Slice(s.readyQueue, func(i, j int) bool {
		return s.readyQueue[i] < s.readyQueue[j]
	})
}

// removeFromReadyQueue removes a TID from the ready queue.
func (s *DSTScheduler) removeFromReadyQueue(tid ThreadID) {
	for i, t := range s.readyQueue {
		if t == tid {
			s.readyQueue = append(s.readyQueue[:i], s.readyQueue[i+1:]...)
			return
		}
	}
}

// isInReadyQueue checks if a TID is in the ready queue.
func (s *DSTScheduler) isInReadyQueue(tid ThreadID) bool {
	for _, t := range s.readyQueue {
		if t == tid {
			return true
		}
	}
	return false
}
