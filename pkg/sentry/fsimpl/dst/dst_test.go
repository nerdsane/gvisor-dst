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

func TestDeterministicClockBasic(t *testing.T) {
	initialTime := int64(1000000000) // 1 second in nanoseconds
	clock := NewDeterministicClock(initialTime)

	if clock.NowNS() != initialTime {
		t.Errorf("NowNS() = %d, want %d", clock.NowNS(), initialTime)
	}

	ktime := clock.Now()
	if ktime.Nanoseconds() != initialTime {
		t.Errorf("Now().Nanoseconds() = %d, want %d", ktime.Nanoseconds(), initialTime)
	}
}

func TestDeterministicClockAdvance(t *testing.T) {
	clock := NewDeterministicClock(0)

	clock.Advance(1000000) // Advance 1ms
	if clock.NowNS() != 1000000 {
		t.Errorf("After advance: NowNS() = %d, want 1000000", clock.NowNS())
	}

	clock.Advance(500000) // Advance another 0.5ms
	if clock.NowNS() != 1500000 {
		t.Errorf("After second advance: NowNS() = %d, want 1500000", clock.NowNS())
	}
}

func TestDeterministicClockSetTime(t *testing.T) {
	clock := NewDeterministicClock(0)

	clock.SetTime(5000000000) // Set to 5 seconds
	if clock.NowNS() != 5000000000 {
		t.Errorf("After SetTime: NowNS() = %d, want 5000000000", clock.NowNS())
	}
}

func TestDeterministicClockCheckpoint(t *testing.T) {
	clock := NewDeterministicClock(1000)
	clock.Advance(500)

	// Save state
	state := clock.GetState()
	if state.TimeNS != 1500 {
		t.Errorf("GetState().TimeNS = %d, want 1500", state.TimeNS)
	}

	// Modify clock
	clock.Advance(1000)
	if clock.NowNS() != 2500 {
		t.Errorf("After more advance: NowNS() = %d, want 2500", clock.NowNS())
	}

	// Restore state
	clock.SetState(state)
	if clock.NowNS() != 1500 {
		t.Errorf("After restore: NowNS() = %d, want 1500", clock.NowNS())
	}
}

func TestDeterministicClockConcurrent(t *testing.T) {
	clock := NewDeterministicClock(0)
	var wg sync.WaitGroup

	// Multiple readers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = clock.NowNS()
			}
		}()
	}

	// Single writer
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 100; j++ {
			clock.Advance(1)
		}
	}()

	wg.Wait()
	// Should not panic or race
}

func TestDeterministicInodeAllocatorBasic(t *testing.T) {
	alloc := NewDeterministicInodeAllocator(1)

	ino1 := alloc.Allocate()
	if ino1 != 1 {
		t.Errorf("First allocation = %d, want 1", ino1)
	}

	ino2 := alloc.Allocate()
	if ino2 != 2 {
		t.Errorf("Second allocation = %d, want 2", ino2)
	}

	ino3 := alloc.Allocate()
	if ino3 != 3 {
		t.Errorf("Third allocation = %d, want 3", ino3)
	}
}

func TestDeterministicInodeAllocatorGetNext(t *testing.T) {
	alloc := NewDeterministicInodeAllocator(100)

	next := alloc.GetNext()
	if next != 100 {
		t.Errorf("GetNext() = %d, want 100", next)
	}

	// GetNext should not change the state
	next = alloc.GetNext()
	if next != 100 {
		t.Errorf("Second GetNext() = %d, want 100", next)
	}

	// Allocate should return and increment
	ino := alloc.Allocate()
	if ino != 100 {
		t.Errorf("Allocate() = %d, want 100", ino)
	}

	next = alloc.GetNext()
	if next != 101 {
		t.Errorf("GetNext() after Allocate = %d, want 101", next)
	}
}

func TestDeterministicInodeAllocatorSetNext(t *testing.T) {
	alloc := NewDeterministicInodeAllocator(1)

	alloc.Allocate() // 1
	alloc.Allocate() // 2

	// Restore to a previous state
	alloc.SetNext(1)

	ino := alloc.Allocate()
	if ino != 1 {
		t.Errorf("After SetNext, Allocate() = %d, want 1", ino)
	}
}

func TestDeterministicInodeAllocatorConcurrent(t *testing.T) {
	alloc := NewDeterministicInodeAllocator(1)
	var wg sync.WaitGroup
	inodes := make(chan uint64, 100)

	// Concurrent allocations
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				inodes <- alloc.Allocate()
			}
		}()
	}

	wg.Wait()
	close(inodes)

	// Check that all inodes are unique
	seen := make(map[uint64]bool)
	for ino := range inodes {
		if seen[ino] {
			t.Errorf("Duplicate inode allocated: %d", ino)
		}
		seen[ino] = true
	}

	if len(seen) != 100 {
		t.Errorf("Got %d unique inodes, want 100", len(seen))
	}
}

func TestSortDirectoryEntriesByName(t *testing.T) {
	entries := []DirectoryEntry{
		{Name: "zebra", Inode: 3, Type: 1},
		{Name: "apple", Inode: 1, Type: 1},
		{Name: "mango", Inode: 2, Type: 1},
	}

	SortDirectoryEntries(entries)

	expected := []string{"apple", "mango", "zebra"}
	for i, e := range entries {
		if e.Name != expected[i] {
			t.Errorf("Entry %d: got %s, want %s", i, e.Name, expected[i])
		}
	}
}

func TestSortDirectoryEntriesByInode(t *testing.T) {
	entries := []DirectoryEntry{
		{Name: "c", Inode: 30, Type: 1},
		{Name: "a", Inode: 10, Type: 1},
		{Name: "b", Inode: 20, Type: 1},
	}

	SortDirectoryEntriesByInode(entries)

	expectedInodes := []uint64{10, 20, 30}
	for i, e := range entries {
		if e.Inode != expectedInodes[i] {
			t.Errorf("Entry %d: got inode %d, want %d", i, e.Inode, expectedInodes[i])
		}
	}
}

func TestDSTFilesystemEnableDisable(t *testing.T) {
	// Start disabled
	DisableDSTFilesystem()
	if IsDSTFilesystemEnabled() {
		t.Error("Expected DST filesystem to be disabled initially")
	}

	// Enable
	config := DSTFilesystemConfig{
		Enabled:              true,
		InitialTimeNS:        1000000000,
		SortDirectoryEntries: true,
		Seed:                 12345,
	}
	EnableDSTFilesystem(config)

	if !IsDSTFilesystemEnabled() {
		t.Error("Expected DST filesystem to be enabled")
	}

	gotConfig := GetDSTFilesystemConfig()
	if gotConfig.InitialTimeNS != config.InitialTimeNS {
		t.Errorf("Config InitialTimeNS = %d, want %d", gotConfig.InitialTimeNS, config.InitialTimeNS)
	}

	// Disable
	DisableDSTFilesystem()
	if IsDSTFilesystemEnabled() {
		t.Error("Expected DST filesystem to be disabled after DisableDSTFilesystem()")
	}
}

func TestDSTFilesystemClock(t *testing.T) {
	DisableDSTFilesystem()

	// Clock should be nil when disabled
	clock := GetDSTClock()
	if clock != nil {
		t.Error("Expected nil clock when disabled")
	}

	// Enable with initial time
	EnableDSTFilesystem(DSTFilesystemConfig{
		Enabled:       true,
		InitialTimeNS: 5000000000,
	})

	clock = GetDSTClock()
	if clock == nil {
		t.Fatal("Expected non-nil clock when enabled")
	}

	if clock.NowNS() != 5000000000 {
		t.Errorf("Clock time = %d, want 5000000000", clock.NowNS())
	}

	DisableDSTFilesystem()
}

func TestDSTFilesystemTimeOperations(t *testing.T) {
	EnableDSTFilesystem(DSTFilesystemConfig{
		Enabled:       true,
		InitialTimeNS: 0,
	})
	defer DisableDSTFilesystem()

	// Advance time
	AdvanceFilesystemTime(1000000) // 1ms
	if GetFilesystemTime() != 1000000 {
		t.Errorf("After advance: time = %d, want 1000000", GetFilesystemTime())
	}

	// Set time
	SetFilesystemTime(5000000000)
	if GetFilesystemTime() != 5000000000 {
		t.Errorf("After set: time = %d, want 5000000000", GetFilesystemTime())
	}
}

func TestShouldSortDirectoryEntries(t *testing.T) {
	DisableDSTFilesystem()

	// Should be false when disabled
	if ShouldSortDirectoryEntries() {
		t.Error("Expected ShouldSortDirectoryEntries to be false when disabled")
	}

	// Enable without sorting
	EnableDSTFilesystem(DSTFilesystemConfig{
		Enabled:              true,
		SortDirectoryEntries: false,
	})
	if ShouldSortDirectoryEntries() {
		t.Error("Expected ShouldSortDirectoryEntries to be false when SortDirectoryEntries=false")
	}

	// Enable with sorting
	EnableDSTFilesystem(DSTFilesystemConfig{
		Enabled:              true,
		SortDirectoryEntries: true,
	})
	if !ShouldSortDirectoryEntries() {
		t.Error("Expected ShouldSortDirectoryEntries to be true")
	}

	DisableDSTFilesystem()
}

func TestDSTFilesystemCheckpoint(t *testing.T) {
	EnableDSTFilesystem(DSTFilesystemConfig{
		Enabled:              true,
		InitialTimeNS:        0,
		SortDirectoryEntries: true,
		Seed:                 42,
	})

	AdvanceFilesystemTime(1000000)

	// Get state
	state := GetDSTFilesystemState()
	if state == nil {
		t.Fatal("Expected non-nil state")
	}
	if state.ClockState.TimeNS != 1000000 {
		t.Errorf("State clock time = %d, want 1000000", state.ClockState.TimeNS)
	}

	// Modify
	AdvanceFilesystemTime(500000)
	if GetFilesystemTime() != 1500000 {
		t.Errorf("After more advance: time = %d, want 1500000", GetFilesystemTime())
	}

	// Restore
	RestoreDSTFilesystemState(state)
	if GetFilesystemTime() != 1000000 {
		t.Errorf("After restore: time = %d, want 1000000", GetFilesystemTime())
	}

	DisableDSTFilesystem()
}

func TestDSTFilesystemRestoreNil(t *testing.T) {
	EnableDSTFilesystem(DSTFilesystemConfig{
		Enabled: true,
	})

	// Restoring nil should disable
	RestoreDSTFilesystemState(nil)

	if IsDSTFilesystemEnabled() {
		t.Error("Expected DST filesystem to be disabled after restoring nil state")
	}
}

type testFilesystemListener struct {
	mu              sync.Mutex
	filesCreated    []string
	filesDeleted    []string
	filesModified   []string
	dirsCreated     []string
	dirsDeleted     []string
}

func (l *testFilesystemListener) OnFileCreated(path string, inode uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.filesCreated = append(l.filesCreated, path)
}

func (l *testFilesystemListener) OnFileDeleted(path string, inode uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.filesDeleted = append(l.filesDeleted, path)
}

func (l *testFilesystemListener) OnFileModified(path string, inode uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.filesModified = append(l.filesModified, path)
}

func (l *testFilesystemListener) OnDirectoryCreated(path string, inode uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.dirsCreated = append(l.dirsCreated, path)
}

func (l *testFilesystemListener) OnDirectoryDeleted(path string, inode uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.dirsDeleted = append(l.dirsDeleted, path)
}

func TestFilesystemListeners(t *testing.T) {
	listener := &testFilesystemListener{}
	AddFilesystemListener(listener)

	NotifyFileCreated("/test/file.txt", 1)
	NotifyFileModified("/test/file.txt", 1)
	NotifyFileDeleted("/test/file.txt", 1)
	NotifyDirectoryCreated("/test/dir", 2)
	NotifyDirectoryDeleted("/test/dir", 2)

	listener.mu.Lock()
	defer listener.mu.Unlock()

	if len(listener.filesCreated) != 1 || listener.filesCreated[0] != "/test/file.txt" {
		t.Errorf("filesCreated = %v, want [/test/file.txt]", listener.filesCreated)
	}
	if len(listener.filesModified) != 1 || listener.filesModified[0] != "/test/file.txt" {
		t.Errorf("filesModified = %v, want [/test/file.txt]", listener.filesModified)
	}
	if len(listener.filesDeleted) != 1 || listener.filesDeleted[0] != "/test/file.txt" {
		t.Errorf("filesDeleted = %v, want [/test/file.txt]", listener.filesDeleted)
	}
	if len(listener.dirsCreated) != 1 || listener.dirsCreated[0] != "/test/dir" {
		t.Errorf("dirsCreated = %v, want [/test/dir]", listener.dirsCreated)
	}
	if len(listener.dirsDeleted) != 1 || listener.dirsDeleted[0] != "/test/dir" {
		t.Errorf("dirsDeleted = %v, want [/test/dir]", listener.dirsDeleted)
	}
}

func TestDeterminism(t *testing.T) {
	// Run the same sequence twice and verify identical results
	runSequence := func() ([]uint64, int64) {
		DisableDSTFilesystem()
		EnableDSTFilesystem(DSTFilesystemConfig{
			Enabled:       true,
			InitialTimeNS: 1000000000,
			Seed:          12345,
		})
		defer DisableDSTFilesystem()

		alloc := NewDeterministicInodeAllocator(1)
		inodes := make([]uint64, 10)
		for i := 0; i < 10; i++ {
			inodes[i] = alloc.Allocate()
			AdvanceFilesystemTime(1000) // 1 microsecond
		}

		return inodes, GetFilesystemTime()
	}

	inodes1, time1 := runSequence()
	inodes2, time2 := runSequence()

	// Verify identical results
	for i := range inodes1 {
		if inodes1[i] != inodes2[i] {
			t.Errorf("Inode mismatch at %d: %d vs %d", i, inodes1[i], inodes2[i])
		}
	}

	if time1 != time2 {
		t.Errorf("Time mismatch: %d vs %d", time1, time2)
	}
}

func BenchmarkInodeAllocation(b *testing.B) {
	alloc := NewDeterministicInodeAllocator(1)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		alloc.Allocate()
	}
}

func BenchmarkClockNow(b *testing.B) {
	clock := NewDeterministicClock(0)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = clock.NowNS()
	}
}

func BenchmarkSortDirectoryEntries(b *testing.B) {
	entries := make([]DirectoryEntry, 100)
	for i := 0; i < 100; i++ {
		entries[i] = DirectoryEntry{
			Name:  string(rune('a' + (99-i)%26)),
			Inode: uint64(100 - i),
			Type:  1,
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Copy to avoid modifying the original
		entriesCopy := make([]DirectoryEntry, len(entries))
		copy(entriesCopy, entries)
		SortDirectoryEntries(entriesCopy)
	}
}
