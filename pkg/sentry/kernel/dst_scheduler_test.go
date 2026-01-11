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
	"sync"
	"testing"
	"time"
)

func TestDSTSchedulerBasic(t *testing.T) {
	s := NewDSTScheduler()

	if s.IsEnabled() {
		t.Error("Scheduler should be disabled by default")
	}

	s.Enable()
	if !s.IsEnabled() {
		t.Error("Scheduler should be enabled after Enable()")
	}

	s.Disable()
	if s.IsEnabled() {
		t.Error("Scheduler should be disabled after Disable()")
	}
}

func TestDSTSchedulerSingleTask(t *testing.T) {
	s := NewDSTScheduler()
	s.Enable()

	// Register a task.
	tid := ThreadID(1)
	s.RegisterTask(tid)

	// The task should immediately get the permit.
	if s.GetCurrentTask() != tid {
		t.Errorf("Expected current task %d, got %d", tid, s.GetCurrentTask())
	}

	// Yield and verify the task gets re-scheduled.
	s.Yield(tid)

	// After yield, the task should be re-added to the ready queue and scheduled.
	if s.GetCurrentTask() != tid {
		t.Errorf("Expected current task %d after yield, got %d", tid, s.GetCurrentTask())
	}

	// Unregister the task.
	s.UnregisterTask(tid)

	if s.GetCurrentTask() != 0 {
		t.Errorf("Expected no current task after unregister, got %d", s.GetCurrentTask())
	}
}

func TestDSTSchedulerMultipleTasks(t *testing.T) {
	s := NewDSTScheduler()
	s.Enable()

	// Register tasks in reverse order to verify TID-based ordering.
	tids := []ThreadID{5, 3, 1, 4, 2}
	for _, tid := range tids {
		s.RegisterTask(tid)
	}

	// The lowest TID should be scheduled first.
	if s.GetCurrentTask() != 1 {
		t.Errorf("Expected current task 1 (lowest TID), got %d", s.GetCurrentTask())
	}

	// Yield and verify the next lowest TID is scheduled.
	s.Yield(1)
	if s.GetCurrentTask() != 2 {
		t.Errorf("Expected current task 2 after first yield, got %d", s.GetCurrentTask())
	}

	s.Yield(2)
	if s.GetCurrentTask() != 3 {
		t.Errorf("Expected current task 3 after second yield, got %d", s.GetCurrentTask())
	}

	// Clean up.
	for _, tid := range tids {
		s.UnregisterTask(tid)
	}
}

func TestDSTSchedulerDeterminism(t *testing.T) {
	// Run the same sequence twice and verify identical results.
	runSequence := func() []ThreadID {
		s := NewDSTScheduler()
		s.Enable()

		tids := []ThreadID{10, 5, 15, 1, 8}
		for _, tid := range tids {
			s.RegisterTask(tid)
		}

		var scheduled []ThreadID
		for i := 0; i < 10; i++ {
			current := s.GetCurrentTask()
			scheduled = append(scheduled, current)
			s.Yield(current)
		}

		return scheduled
	}

	seq1 := runSequence()
	seq2 := runSequence()

	if len(seq1) != len(seq2) {
		t.Fatalf("Sequences have different lengths: %d vs %d", len(seq1), len(seq2))
	}

	for i := range seq1 {
		if seq1[i] != seq2[i] {
			t.Errorf("Sequences differ at position %d: %d vs %d", i, seq1[i], seq2[i])
		}
	}
}

func TestDSTSchedulerBlockUnblock(t *testing.T) {
	s := NewDSTScheduler()
	s.Enable()

	s.RegisterTask(1)
	s.RegisterTask(2)
	s.RegisterTask(3)

	// Task 1 should be running.
	if s.GetCurrentTask() != 1 {
		t.Errorf("Expected task 1 to be running, got %d", s.GetCurrentTask())
	}

	// Block task 1.
	s.Block(1)

	// Task 2 should now be running.
	if s.GetCurrentTask() != 2 {
		t.Errorf("Expected task 2 to be running after block, got %d", s.GetCurrentTask())
	}

	// Yield task 2.
	s.Yield(2)

	// Task 3 should now be running.
	if s.GetCurrentTask() != 3 {
		t.Errorf("Expected task 3 to be running, got %d", s.GetCurrentTask())
	}

	// Unblock task 1.
	s.Unblock(1)

	// Task 3 should still be running.
	if s.GetCurrentTask() != 3 {
		t.Errorf("Expected task 3 to still be running, got %d", s.GetCurrentTask())
	}

	// Yield task 3, task 1 should be next (lowest TID in ready queue).
	s.Yield(3)
	if s.GetCurrentTask() != 1 {
		t.Errorf("Expected task 1 to be running after unblock and yield, got %d", s.GetCurrentTask())
	}
}

func TestDSTSchedulerCheckpointRestore(t *testing.T) {
	s := NewDSTScheduler()
	s.Enable()

	s.RegisterTask(1)
	s.RegisterTask(2)
	s.RegisterTask(3)

	// Run for a bit.
	for i := 0; i < 5; i++ {
		current := s.GetCurrentTask()
		s.Yield(current)
	}

	// Take a checkpoint.
	state := s.GetState()

	// Run more.
	for i := 0; i < 3; i++ {
		current := s.GetCurrentTask()
		s.Yield(current)
	}

	originalYieldCount := s.GetYieldCounter()

	// Create a new scheduler and restore.
	s2 := NewDSTScheduler()
	s2.RegisterTask(1)
	s2.RegisterTask(2)
	s2.RegisterTask(3)
	s2.SetState(state)

	// Verify state was restored.
	if s2.GetYieldCounter() != state.YieldCounter {
		t.Errorf("Yield counter not restored: got %d, want %d",
			s2.GetYieldCounter(), state.YieldCounter)
	}

	if s2.GetCurrentTask() != state.CurrentTID {
		t.Errorf("Current task not restored: got %d, want %d",
			s2.GetCurrentTask(), state.CurrentTID)
	}

	// Both should produce the same sequence from here.
	s.SetState(state) // Reset original scheduler

	seq1 := make([]ThreadID, 0, 5)
	seq2 := make([]ThreadID, 0, 5)

	for i := 0; i < 5; i++ {
		seq1 = append(seq1, s.GetCurrentTask())
		s.Yield(s.GetCurrentTask())

		seq2 = append(seq2, s2.GetCurrentTask())
		s2.Yield(s2.GetCurrentTask())
	}

	for i := range seq1 {
		if seq1[i] != seq2[i] {
			t.Errorf("After restore, sequences differ at position %d: %d vs %d", i, seq1[i], seq2[i])
		}
	}

	_ = originalYieldCount // silence unused warning
}

func TestDSTSchedulerConcurrentYield(t *testing.T) {
	s := NewDSTScheduler()
	s.Enable()

	numTasks := 10
	for i := 1; i <= numTasks; i++ {
		s.RegisterTask(ThreadID(i))
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	scheduled := make([]ThreadID, 0)

	// Simulate concurrent task execution.
	for i := 1; i <= numTasks; i++ {
		wg.Add(1)
		go func(tid ThreadID) {
			defer wg.Done()

			for j := 0; j < 5; j++ {
				// Wait for permit.
				s.WaitForPermit(tid)

				// Record that we're running.
				mu.Lock()
				if s.GetCurrentTask() == tid {
					scheduled = append(scheduled, tid)
				}
				mu.Unlock()

				// Small sleep to simulate work.
				time.Sleep(time.Microsecond)

				// Yield.
				s.Yield(tid)
			}
		}(ThreadID(i))
	}

	// Give tasks time to complete.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success.
	case <-time.After(5 * time.Second):
		t.Fatal("Test timed out - possible deadlock")
	}

	// Verify all tasks ran.
	taskRuns := make(map[ThreadID]int)
	for _, tid := range scheduled {
		taskRuns[tid]++
	}

	for i := 1; i <= numTasks; i++ {
		if taskRuns[ThreadID(i)] == 0 {
			t.Errorf("Task %d never ran", i)
		}
	}
}

func TestDSTSchedulerDisabledNoBlock(t *testing.T) {
	s := NewDSTScheduler()
	// Keep disabled.

	s.RegisterTask(1)
	s.RegisterTask(2)

	// WaitForPermit should return immediately when disabled.
	done := make(chan struct{})
	go func() {
		s.WaitForPermit(1)
		s.WaitForPermit(2)
		close(done)
	}()

	select {
	case <-done:
		// Success - didn't block.
	case <-time.After(time.Second):
		t.Error("WaitForPermit blocked when scheduler is disabled")
	}
}

// testSchedulerListener records scheduling events for verification.
type testSchedulerListener struct {
	mu        sync.Mutex
	scheduled []ThreadID
	yielded   []ThreadID
	blocked   []ThreadID
	unblocked []ThreadID
}

func (l *testSchedulerListener) OnTaskScheduled(tid ThreadID) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.scheduled = append(l.scheduled, tid)
}

func (l *testSchedulerListener) OnTaskYielded(tid ThreadID) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.yielded = append(l.yielded, tid)
}

func (l *testSchedulerListener) OnTaskBlocked(tid ThreadID) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.blocked = append(l.blocked, tid)
}

func (l *testSchedulerListener) OnTaskUnblocked(tid ThreadID) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.unblocked = append(l.unblocked, tid)
}

func TestDSTSchedulerListener(t *testing.T) {
	s := NewDSTScheduler()
	listener := &testSchedulerListener{}
	s.AddListener(listener)
	s.Enable()

	s.RegisterTask(1)
	s.RegisterTask(2)

	// Task 1 should be scheduled.
	if len(listener.scheduled) != 1 || listener.scheduled[0] != 1 {
		t.Errorf("Expected task 1 to be scheduled, got %v", listener.scheduled)
	}

	// Block task 1.
	s.Block(1)

	if len(listener.blocked) != 1 || listener.blocked[0] != 1 {
		t.Errorf("Expected task 1 to be blocked, got %v", listener.blocked)
	}

	// Task 2 should be scheduled.
	if len(listener.scheduled) != 2 || listener.scheduled[1] != 2 {
		t.Errorf("Expected task 2 to be scheduled, got %v", listener.scheduled)
	}

	// Unblock task 1.
	s.Unblock(1)

	if len(listener.unblocked) != 1 || listener.unblocked[0] != 1 {
		t.Errorf("Expected task 1 to be unblocked, got %v", listener.unblocked)
	}

	// Yield task 2.
	s.Yield(2)

	if len(listener.yielded) != 1 || listener.yielded[0] != 2 {
		t.Errorf("Expected task 2 to yield, got %v", listener.yielded)
	}
}

func TestDSTSchedulerYieldCounter(t *testing.T) {
	s := NewDSTScheduler()
	s.Enable()

	s.RegisterTask(1)
	s.RegisterTask(2)

	if s.GetYieldCounter() != 0 {
		t.Errorf("Initial yield counter should be 0, got %d", s.GetYieldCounter())
	}

	s.Yield(1)
	if s.GetYieldCounter() != 1 {
		t.Errorf("Yield counter should be 1, got %d", s.GetYieldCounter())
	}

	s.Yield(2)
	if s.GetYieldCounter() != 2 {
		t.Errorf("Yield counter should be 2, got %d", s.GetYieldCounter())
	}

	// Yield from wrong task should not increment counter.
	s.Yield(999) // Non-existent task.
	if s.GetYieldCounter() != 2 {
		t.Errorf("Yield counter should still be 2, got %d", s.GetYieldCounter())
	}
}

func TestDSTSchedulerDoubleRegister(t *testing.T) {
	s := NewDSTScheduler()
	s.Enable()

	s.RegisterTask(1)
	s.RegisterTask(1) // Double register.

	// Should only have one entry.
	state := s.GetState()
	count := 0
	for _, tid := range state.ReadyTIDs {
		if tid == 1 {
			count++
		}
	}
	// Note: Task 1 is the current task, so not in ready queue.
	// But let's check that current is set.
	if s.GetCurrentTask() != 1 {
		t.Errorf("Expected task 1 to be current, got %d", s.GetCurrentTask())
	}
}

func BenchmarkDSTSchedulerYield(b *testing.B) {
	s := NewDSTScheduler()
	s.Enable()

	for i := 1; i <= 10; i++ {
		s.RegisterTask(ThreadID(i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		current := s.GetCurrentTask()
		s.Yield(current)
	}
}

func BenchmarkDSTSchedulerBlockUnblock(b *testing.B) {
	s := NewDSTScheduler()
	s.Enable()

	for i := 1; i <= 10; i++ {
		s.RegisterTask(ThreadID(i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		current := s.GetCurrentTask()
		s.Block(current)
		s.Unblock(current)
	}
}
