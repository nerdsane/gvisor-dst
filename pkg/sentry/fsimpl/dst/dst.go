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

// Package dst provides deterministic filesystem support for simulation testing.
// It ensures filesystem operations produce reproducible results by:
// - Using virtual time for all timestamps
// - Providing ordered directory iteration
// - Deterministic inode allocation
package dst

import (
	"sort"
	"time"

	"gvisor.dev/gvisor/pkg/sentry/ktime"
	"gvisor.dev/gvisor/pkg/sync"
)

// DSTFilesystemConfig holds configuration for deterministic filesystem mode.
type DSTFilesystemConfig struct {
	// Enabled indicates whether DST filesystem mode is active.
	Enabled bool

	// InitialTimeNS is the initial filesystem time in nanoseconds since epoch.
	InitialTimeNS int64

	// SortDirectoryEntries controls whether directory entries are sorted.
	SortDirectoryEntries bool

	// Seed is the random seed for any filesystem operations requiring randomness.
	Seed uint64
}

// dstGlobalState holds global DST filesystem state.
var dstGlobalState struct {
	mu     sync.RWMutex
	config DSTFilesystemConfig
	clock  *DeterministicClock
}

// EnableDSTFilesystem enables deterministic filesystem mode.
func EnableDSTFilesystem(config DSTFilesystemConfig) {
	dstGlobalState.mu.Lock()
	defer dstGlobalState.mu.Unlock()

	dstGlobalState.config = config

	if config.Enabled {
		dstGlobalState.clock = NewDeterministicClock(config.InitialTimeNS)
	}
}

// DisableDSTFilesystem disables deterministic filesystem mode.
func DisableDSTFilesystem() {
	dstGlobalState.mu.Lock()
	defer dstGlobalState.mu.Unlock()

	dstGlobalState.config.Enabled = false
	dstGlobalState.clock = nil
}

// IsDSTFilesystemEnabled returns true if DST filesystem mode is enabled.
func IsDSTFilesystemEnabled() bool {
	dstGlobalState.mu.RLock()
	defer dstGlobalState.mu.RUnlock()
	return dstGlobalState.config.Enabled
}

// GetDSTClock returns the deterministic clock, or nil if not enabled.
func GetDSTClock() *DeterministicClock {
	dstGlobalState.mu.RLock()
	defer dstGlobalState.mu.RUnlock()
	return dstGlobalState.clock
}

// GetDSTFilesystemConfig returns the current DST filesystem configuration.
func GetDSTFilesystemConfig() DSTFilesystemConfig {
	dstGlobalState.mu.RLock()
	defer dstGlobalState.mu.RUnlock()
	return dstGlobalState.config
}

// ShouldSortDirectoryEntries returns true if directory entries should be sorted.
func ShouldSortDirectoryEntries() bool {
	dstGlobalState.mu.RLock()
	defer dstGlobalState.mu.RUnlock()
	return dstGlobalState.config.Enabled && dstGlobalState.config.SortDirectoryEntries
}

// DSTFilesystemState represents the filesystem state for checkpointing.
type DSTFilesystemState struct {
	Config     DSTFilesystemConfig
	ClockState DeterministicClockState
}

// GetDSTFilesystemState returns the current DST filesystem state for checkpointing.
func GetDSTFilesystemState() *DSTFilesystemState {
	dstGlobalState.mu.RLock()
	defer dstGlobalState.mu.RUnlock()

	if !dstGlobalState.config.Enabled || dstGlobalState.clock == nil {
		return nil
	}

	return &DSTFilesystemState{
		Config:     dstGlobalState.config,
		ClockState: dstGlobalState.clock.GetState(),
	}
}

// RestoreDSTFilesystemState restores DST filesystem state from a checkpoint.
func RestoreDSTFilesystemState(state *DSTFilesystemState) {
	if state == nil {
		DisableDSTFilesystem()
		return
	}

	dstGlobalState.mu.Lock()
	defer dstGlobalState.mu.Unlock()

	dstGlobalState.config = state.Config

	if state.Config.Enabled {
		if dstGlobalState.clock == nil {
			dstGlobalState.clock = NewDeterministicClock(state.Config.InitialTimeNS)
		}
		dstGlobalState.clock.SetState(state.ClockState)
	}
}

// AdvanceFilesystemTime advances the filesystem clock by the given duration.
func AdvanceFilesystemTime(deltaNS int64) {
	dstGlobalState.mu.RLock()
	clock := dstGlobalState.clock
	dstGlobalState.mu.RUnlock()

	if clock != nil {
		clock.Advance(deltaNS)
	}
}

// SetFilesystemTime sets the filesystem clock to a specific time.
func SetFilesystemTime(timeNS int64) {
	dstGlobalState.mu.RLock()
	clock := dstGlobalState.clock
	dstGlobalState.mu.RUnlock()

	if clock != nil {
		clock.SetTime(timeNS)
	}
}

// GetFilesystemTime returns the current filesystem time in nanoseconds.
func GetFilesystemTime() int64 {
	dstGlobalState.mu.RLock()
	clock := dstGlobalState.clock
	dstGlobalState.mu.RUnlock()

	if clock != nil {
		return clock.NowNS()
	}
	return time.Now().UnixNano()
}

// DeterministicClock implements ktime.Clock with deterministic time.
type DeterministicClock struct {
	mu sync.RWMutex
	// +checklocks:mu
	timeNS int64
}

// DeterministicClockState represents the clock state for checkpointing.
type DeterministicClockState struct {
	TimeNS int64
}

// NewDeterministicClock creates a new deterministic clock.
func NewDeterministicClock(initialTimeNS int64) *DeterministicClock {
	return &DeterministicClock{
		timeNS: initialTimeNS,
	}
}

// Now implements ktime.Clock.Now.
func (c *DeterministicClock) Now() ktime.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return ktime.FromNanoseconds(c.timeNS)
}

// NowNS returns the current time in nanoseconds.
func (c *DeterministicClock) NowNS() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.timeNS
}

// Advance advances the clock by the given number of nanoseconds.
func (c *DeterministicClock) Advance(deltaNS int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.timeNS += deltaNS
}

// SetTime sets the clock to a specific time.
func (c *DeterministicClock) SetTime(timeNS int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.timeNS = timeNS
}

// GetState returns the clock state for checkpointing.
func (c *DeterministicClock) GetState() DeterministicClockState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return DeterministicClockState{TimeNS: c.timeNS}
}

// SetState restores the clock state from a checkpoint.
func (c *DeterministicClock) SetState(state DeterministicClockState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.timeNS = state.TimeNS
}

// DirectoryEntry represents a directory entry for sorting.
type DirectoryEntry struct {
	Name  string
	Inode uint64
	Type  uint8
}

// SortDirectoryEntries sorts directory entries by name for deterministic iteration.
func SortDirectoryEntries(entries []DirectoryEntry) {
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})
}

// SortDirectoryEntriesByInode sorts directory entries by inode for deterministic iteration.
func SortDirectoryEntriesByInode(entries []DirectoryEntry) {
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Inode < entries[j].Inode
	})
}

// DeterministicInodeAllocator provides deterministic inode number allocation.
type DeterministicInodeAllocator struct {
	mu sync.Mutex
	// +checklocks:mu
	nextInode uint64
}

// NewDeterministicInodeAllocator creates a new inode allocator.
func NewDeterministicInodeAllocator(start uint64) *DeterministicInodeAllocator {
	return &DeterministicInodeAllocator{
		nextInode: start,
	}
}

// Allocate returns the next inode number.
func (a *DeterministicInodeAllocator) Allocate() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	ino := a.nextInode
	a.nextInode++
	return ino
}

// GetNext returns the next inode number without allocating it.
func (a *DeterministicInodeAllocator) GetNext() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.nextInode
}

// SetNext sets the next inode number (for checkpoint restore).
func (a *DeterministicInodeAllocator) SetNext(next uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.nextInode = next
}

// DSTFilesystemListener is notified of filesystem events.
type DSTFilesystemListener interface {
	OnFileCreated(path string, inode uint64)
	OnFileDeleted(path string, inode uint64)
	OnFileModified(path string, inode uint64)
	OnDirectoryCreated(path string, inode uint64)
	OnDirectoryDeleted(path string, inode uint64)
}

// dstListeners holds registered filesystem listeners.
var dstListeners struct {
	mu        sync.RWMutex
	listeners []DSTFilesystemListener
}

// AddFilesystemListener adds a listener for filesystem events.
func AddFilesystemListener(l DSTFilesystemListener) {
	dstListeners.mu.Lock()
	defer dstListeners.mu.Unlock()
	dstListeners.listeners = append(dstListeners.listeners, l)
}

// NotifyFileCreated notifies listeners of a file creation.
func NotifyFileCreated(path string, inode uint64) {
	dstListeners.mu.RLock()
	defer dstListeners.mu.RUnlock()
	for _, l := range dstListeners.listeners {
		l.OnFileCreated(path, inode)
	}
}

// NotifyFileDeleted notifies listeners of a file deletion.
func NotifyFileDeleted(path string, inode uint64) {
	dstListeners.mu.RLock()
	defer dstListeners.mu.RUnlock()
	for _, l := range dstListeners.listeners {
		l.OnFileDeleted(path, inode)
	}
}

// NotifyFileModified notifies listeners of a file modification.
func NotifyFileModified(path string, inode uint64) {
	dstListeners.mu.RLock()
	defer dstListeners.mu.RUnlock()
	for _, l := range dstListeners.listeners {
		l.OnFileModified(path, inode)
	}
}

// NotifyDirectoryCreated notifies listeners of a directory creation.
func NotifyDirectoryCreated(path string, inode uint64) {
	dstListeners.mu.RLock()
	defer dstListeners.mu.RUnlock()
	for _, l := range dstListeners.listeners {
		l.OnDirectoryCreated(path, inode)
	}
}

// NotifyDirectoryDeleted notifies listeners of a directory deletion.
func NotifyDirectoryDeleted(path string, inode uint64) {
	dstListeners.mu.RLock()
	defer dstListeners.mu.RUnlock()
	for _, l := range dstListeners.listeners {
		l.OnDirectoryDeleted(path, inode)
	}
}
