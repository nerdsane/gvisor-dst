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

// Package dst provides deterministic simulation testing infrastructure
// including fast snapshot/restore capabilities for state space exploration.
package dst

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"gvisor.dev/gvisor/pkg/sync"
)

// SnapshotID uniquely identifies a snapshot in the tree.
type SnapshotID uint64

// InvalidSnapshotID represents an invalid/unset snapshot ID.
const InvalidSnapshotID SnapshotID = 0

// SnapshotMetadata holds metadata about a snapshot.
type SnapshotMetadata struct {
	// ID is the unique identifier for this snapshot.
	ID SnapshotID

	// ParentID is the ID of the parent snapshot (0 if root).
	ParentID SnapshotID

	// Seed is the random seed used at this snapshot point.
	Seed uint64

	// VirtualTimeNS is the virtual time at snapshot creation.
	VirtualTimeNS int64

	// StepCount is the number of simulation steps at snapshot creation.
	StepCount uint64

	// Description is an optional human-readable description.
	Description string

	// Tags are optional key-value pairs for categorization.
	Tags map[string]string
}

// DSTState represents the complete DST state for snapshot/restore.
// This combines all DST component states into a single structure.
type DSTState struct {
	// Metadata about this snapshot.
	Metadata SnapshotMetadata

	// TimeState holds virtual clock state.
	TimeState *VirtualTimeState

	// RNGState holds deterministic RNG state.
	RNGState *RNGState

	// SchedulerState holds deterministic scheduler state.
	SchedulerState *SchedulerState

	// NetworkState holds deterministic network state.
	NetworkState *NetworkState

	// FilesystemState holds deterministic filesystem state.
	FilesystemState *FilesystemState

	// ApplicationState holds arbitrary application-specific state.
	ApplicationState map[string][]byte
}

// VirtualTimeState captures virtual clock state.
type VirtualTimeState struct {
	MonotonicNS int64
	RealtimeNS  int64
	Cycles      uint64
}

// RNGState captures deterministic RNG state.
type RNGState struct {
	// ChaCha20 state (256-bit key + 64-bit counter + buffer)
	State   [32]byte
	Counter uint64
	Buffer  []byte
	BufPos  int
}

// SchedulerState captures deterministic scheduler state.
type SchedulerState struct {
	// TaskStates maps task IDs to their scheduling state.
	TaskStates map[int32]TaskState

	// ReadyQueue is the ordered list of ready task IDs.
	ReadyQueue []int32

	// CurrentTask is the currently running task ID.
	CurrentTask int32

	// YieldCounter tracks total yields for determinism verification.
	YieldCounter uint64
}

// TaskState represents the scheduling state of a single task.
type TaskState struct {
	TID      int32
	State    string // "ready", "running", "blocked"
	Priority int32
}

// NetworkState captures deterministic network state.
type NetworkState struct {
	// EndpointStates maps endpoint IDs to their state.
	EndpointStates map[uint64]EndpointState

	// PendingPackets is the list of packets waiting for delivery.
	PendingPackets []PacketState

	// NextPacketID is the next packet sequence number.
	NextPacketID uint64

	// CurrentTimeNS is the current network virtual time.
	CurrentTimeNS int64

	// Config holds network configuration.
	Config NetworkConfig
}

// EndpointState represents the state of a network endpoint.
type EndpointState struct {
	ID         uint64
	MTU        uint32
	MACAddress string
	Connected  []uint64
}

// PacketState represents a pending packet.
type PacketState struct {
	ID           uint64
	SrcEndpoint  uint64
	DstEndpoint  uint64
	Data         []byte
	DeliveryTime int64
}

// NetworkConfig holds network configuration.
type NetworkConfig struct {
	DefaultDelayNS           int64
	PacketLossProbability    float64
	PacketReorderProbability float64
}

// FilesystemState captures deterministic filesystem state.
type FilesystemState struct {
	// ClockTimeNS is the filesystem clock time.
	ClockTimeNS int64

	// NextInode is the next inode number to allocate.
	NextInode uint64

	// Config holds filesystem configuration.
	Config FilesystemConfig
}

// FilesystemConfig holds filesystem configuration.
type FilesystemConfig struct {
	Enabled              bool
	SortDirectoryEntries bool
	Seed                 uint64
}

// Snapshot represents a complete DST snapshot with CoW support.
type Snapshot struct {
	// State is the captured DST state.
	State *DSTState

	// MemoryPages holds CoW memory pages (page address -> data).
	// In a full implementation, this would reference shared pages.
	MemoryPages *CoWPageStore

	// refCount tracks references for memory management.
	refCount int32
}

// CoWPageStore implements copy-on-write page storage.
type CoWPageStore struct {
	mu sync.RWMutex

	// pages maps page addresses to page data.
	// +checklocks:mu
	pages map[uint64]*CoWPage

	// parent is the parent store for CoW lookups.
	// +checklocks:mu
	parent *CoWPageStore

	// refCount tracks references.
	// +checklocks:mu
	refCount int32
}

// CoWPage represents a single memory page with reference counting.
type CoWPage struct {
	// Data is the page content.
	Data []byte

	// refCount tracks how many snapshots reference this page.
	refCount int32
}

// PageSize is the standard page size (4KB).
const PageSize = 4096

// NewCoWPageStore creates a new CoW page store.
func NewCoWPageStore() *CoWPageStore {
	return &CoWPageStore{
		pages:    make(map[uint64]*CoWPage),
		refCount: 1,
	}
}

// Fork creates a child CoW store that shares pages with the parent.
func (s *CoWPageStore) Fork() *CoWPageStore {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.refCount++
	return &CoWPageStore{
		pages:    make(map[uint64]*CoWPage),
		parent:   s,
		refCount: 1,
	}
}

// GetPage retrieves a page, checking parent stores if necessary.
func (s *CoWPageStore) GetPage(addr uint64) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Check local pages first
	if page, ok := s.pages[addr]; ok {
		return page.Data, true
	}

	// Check parent chain
	if s.parent != nil {
		return s.parent.GetPage(addr)
	}

	return nil, false
}

// SetPage writes a page, performing copy-on-write if necessary.
func (s *CoWPageStore) SetPage(addr uint64, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Create a copy of the data
	pageCopy := make([]byte, len(data))
	copy(pageCopy, data)

	s.pages[addr] = &CoWPage{
		Data:     pageCopy,
		refCount: 1,
	}
}

// Release decrements the reference count and frees resources if zero.
func (s *CoWPageStore) Release() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.refCount--
	if s.refCount <= 0 {
		// Release parent reference
		if s.parent != nil {
			s.parent.Release()
		}
		// Clear pages
		s.pages = nil
	}
}

// PageCount returns the number of pages stored locally (not including parent).
func (s *CoWPageStore) PageCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.pages)
}

// TotalPageCount returns the total number of unique pages including parent chain.
func (s *CoWPageStore) TotalPageCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	count := len(s.pages)
	if s.parent != nil {
		// Count parent pages not overwritten locally
		parentCount := s.parent.TotalPageCount()
		count += parentCount
	}
	return count
}

// SnapshotTree manages a tree of snapshots for state space exploration.
type SnapshotTree struct {
	mu sync.RWMutex

	// snapshots maps snapshot IDs to snapshots.
	// +checklocks:mu
	snapshots map[SnapshotID]*Snapshot

	// children maps parent IDs to child IDs.
	// +checklocks:mu
	children map[SnapshotID][]SnapshotID

	// nextID is the next snapshot ID to assign.
	// +checklocks:mu
	nextID SnapshotID

	// rootID is the ID of the root snapshot.
	// +checklocks:mu
	rootID SnapshotID

	// currentID is the ID of the currently active snapshot.
	// +checklocks:mu
	currentID SnapshotID

	// maxSnapshots is the maximum number of snapshots to retain.
	// +checklocks:mu
	maxSnapshots int
}

// NewSnapshotTree creates a new snapshot tree.
func NewSnapshotTree(maxSnapshots int) *SnapshotTree {
	if maxSnapshots <= 0 {
		maxSnapshots = 10000 // Default max
	}
	return &SnapshotTree{
		snapshots:    make(map[SnapshotID]*Snapshot),
		children:     make(map[SnapshotID][]SnapshotID),
		nextID:       1,
		maxSnapshots: maxSnapshots,
	}
}

// CreateSnapshot creates a new snapshot as a child of the current snapshot.
func (t *SnapshotTree) CreateSnapshot(state *DSTState, pages *CoWPageStore) (SnapshotID, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.snapshots) >= t.maxSnapshots {
		return InvalidSnapshotID, errors.New("snapshot limit reached")
	}

	id := t.nextID
	t.nextID++

	state.Metadata.ID = id
	state.Metadata.ParentID = t.currentID

	snapshot := &Snapshot{
		State:       state,
		MemoryPages: pages,
		refCount:    1,
	}

	t.snapshots[id] = snapshot

	// Track parent-child relationship
	if t.currentID != InvalidSnapshotID {
		t.children[t.currentID] = append(t.children[t.currentID], id)
	} else {
		// This is the root snapshot
		t.rootID = id
	}

	t.currentID = id

	return id, nil
}

// GetSnapshot retrieves a snapshot by ID.
func (t *SnapshotTree) GetSnapshot(id SnapshotID) (*Snapshot, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	snapshot, ok := t.snapshots[id]
	if !ok {
		return nil, fmt.Errorf("snapshot %d not found", id)
	}
	return snapshot, nil
}

// RestoreSnapshot restores the simulation to a given snapshot.
func (t *SnapshotTree) RestoreSnapshot(id SnapshotID) (*DSTState, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	snapshot, ok := t.snapshots[id]
	if !ok {
		return nil, fmt.Errorf("snapshot %d not found", id)
	}

	t.currentID = id

	// Return a copy of the state to prevent modification
	return copyDSTState(snapshot.State), nil
}

// GetCurrentID returns the current snapshot ID.
func (t *SnapshotTree) GetCurrentID() SnapshotID {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.currentID
}

// GetRootID returns the root snapshot ID.
func (t *SnapshotTree) GetRootID() SnapshotID {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.rootID
}

// GetChildren returns the child snapshot IDs for a given parent.
func (t *SnapshotTree) GetChildren(parentID SnapshotID) []SnapshotID {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return append([]SnapshotID{}, t.children[parentID]...)
}

// GetPath returns the path from root to the given snapshot.
func (t *SnapshotTree) GetPath(id SnapshotID) ([]SnapshotID, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var path []SnapshotID
	current := id

	for current != InvalidSnapshotID {
		snapshot, ok := t.snapshots[current]
		if !ok {
			return nil, fmt.Errorf("snapshot %d not found in path", current)
		}
		path = append([]SnapshotID{current}, path...)
		current = snapshot.State.Metadata.ParentID
	}

	return path, nil
}

// GetDepth returns the depth of a snapshot in the tree.
func (t *SnapshotTree) GetDepth(id SnapshotID) (int, error) {
	path, err := t.GetPath(id)
	if err != nil {
		return 0, err
	}
	return len(path), nil
}

// DeleteSnapshot removes a snapshot if it has no children.
func (t *SnapshotTree) DeleteSnapshot(id SnapshotID) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Check if snapshot exists
	snapshot, ok := t.snapshots[id]
	if !ok {
		return fmt.Errorf("snapshot %d not found", id)
	}

	// Check if it has children
	if len(t.children[id]) > 0 {
		return fmt.Errorf("cannot delete snapshot %d: has %d children", id, len(t.children[id]))
	}

	// Remove from parent's children list
	parentID := snapshot.State.Metadata.ParentID
	if parentID != InvalidSnapshotID {
		children := t.children[parentID]
		for i, childID := range children {
			if childID == id {
				t.children[parentID] = append(children[:i], children[i+1:]...)
				break
			}
		}
	}

	// Release CoW pages
	if snapshot.MemoryPages != nil {
		snapshot.MemoryPages.Release()
	}

	delete(t.snapshots, id)
	delete(t.children, id)

	// Update current if we deleted it
	if t.currentID == id {
		t.currentID = parentID
	}

	return nil
}

// PruneLeaves removes all leaf snapshots except those on the path to current.
func (t *SnapshotTree) PruneLeaves() int {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Get path to current
	currentPath := make(map[SnapshotID]bool)
	current := t.currentID
	for current != InvalidSnapshotID {
		currentPath[current] = true
		if snapshot, ok := t.snapshots[current]; ok {
			current = snapshot.State.Metadata.ParentID
		} else {
			break
		}
	}

	// Find leaves to prune
	var toDelete []SnapshotID
	for id := range t.snapshots {
		if len(t.children[id]) == 0 && !currentPath[id] {
			toDelete = append(toDelete, id)
		}
	}

	// Delete leaves
	for _, id := range toDelete {
		snapshot := t.snapshots[id]
		parentID := snapshot.State.Metadata.ParentID

		// Remove from parent's children list
		if parentID != InvalidSnapshotID {
			children := t.children[parentID]
			for i, childID := range children {
				if childID == id {
					t.children[parentID] = append(children[:i], children[i+1:]...)
					break
				}
			}
		}

		// Release CoW pages
		if snapshot.MemoryPages != nil {
			snapshot.MemoryPages.Release()
		}

		delete(t.snapshots, id)
		delete(t.children, id)
	}

	return len(toDelete)
}

// SnapshotCount returns the number of snapshots in the tree.
func (t *SnapshotTree) SnapshotCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.snapshots)
}

// copyDSTState creates a deep copy of DSTState.
func copyDSTState(src *DSTState) *DSTState {
	if src == nil {
		return nil
	}

	dst := &DSTState{
		Metadata: src.Metadata,
	}

	// Copy tags
	if src.Metadata.Tags != nil {
		dst.Metadata.Tags = make(map[string]string)
		for k, v := range src.Metadata.Tags {
			dst.Metadata.Tags[k] = v
		}
	}

	// Copy time state
	if src.TimeState != nil {
		dst.TimeState = &VirtualTimeState{
			MonotonicNS: src.TimeState.MonotonicNS,
			RealtimeNS:  src.TimeState.RealtimeNS,
			Cycles:      src.TimeState.Cycles,
		}
	}

	// Copy RNG state
	if src.RNGState != nil {
		dst.RNGState = &RNGState{
			State:   src.RNGState.State,
			Counter: src.RNGState.Counter,
			BufPos:  src.RNGState.BufPos,
		}
		if src.RNGState.Buffer != nil {
			dst.RNGState.Buffer = make([]byte, len(src.RNGState.Buffer))
			copy(dst.RNGState.Buffer, src.RNGState.Buffer)
		}
	}

	// Copy scheduler state
	if src.SchedulerState != nil {
		dst.SchedulerState = &SchedulerState{
			CurrentTask:  src.SchedulerState.CurrentTask,
			YieldCounter: src.SchedulerState.YieldCounter,
		}
		if src.SchedulerState.TaskStates != nil {
			dst.SchedulerState.TaskStates = make(map[int32]TaskState)
			for k, v := range src.SchedulerState.TaskStates {
				dst.SchedulerState.TaskStates[k] = v
			}
		}
		if src.SchedulerState.ReadyQueue != nil {
			dst.SchedulerState.ReadyQueue = make([]int32, len(src.SchedulerState.ReadyQueue))
			copy(dst.SchedulerState.ReadyQueue, src.SchedulerState.ReadyQueue)
		}
	}

	// Copy network state
	if src.NetworkState != nil {
		dst.NetworkState = &NetworkState{
			NextPacketID:  src.NetworkState.NextPacketID,
			CurrentTimeNS: src.NetworkState.CurrentTimeNS,
			Config:        src.NetworkState.Config,
		}
		if src.NetworkState.EndpointStates != nil {
			dst.NetworkState.EndpointStates = make(map[uint64]EndpointState)
			for k, v := range src.NetworkState.EndpointStates {
				ep := EndpointState{
					ID:         v.ID,
					MTU:        v.MTU,
					MACAddress: v.MACAddress,
				}
				if v.Connected != nil {
					ep.Connected = make([]uint64, len(v.Connected))
					copy(ep.Connected, v.Connected)
				}
				dst.NetworkState.EndpointStates[k] = ep
			}
		}
		if src.NetworkState.PendingPackets != nil {
			dst.NetworkState.PendingPackets = make([]PacketState, len(src.NetworkState.PendingPackets))
			for i, p := range src.NetworkState.PendingPackets {
				dst.NetworkState.PendingPackets[i] = PacketState{
					ID:           p.ID,
					SrcEndpoint:  p.SrcEndpoint,
					DstEndpoint:  p.DstEndpoint,
					DeliveryTime: p.DeliveryTime,
				}
				if p.Data != nil {
					dst.NetworkState.PendingPackets[i].Data = make([]byte, len(p.Data))
					copy(dst.NetworkState.PendingPackets[i].Data, p.Data)
				}
			}
		}
	}

	// Copy filesystem state
	if src.FilesystemState != nil {
		dst.FilesystemState = &FilesystemState{
			ClockTimeNS: src.FilesystemState.ClockTimeNS,
			NextInode:   src.FilesystemState.NextInode,
			Config:      src.FilesystemState.Config,
		}
	}

	// Copy application state
	if src.ApplicationState != nil {
		dst.ApplicationState = make(map[string][]byte)
		for k, v := range src.ApplicationState {
			if v != nil {
				vCopy := make([]byte, len(v))
				copy(vCopy, v)
				dst.ApplicationState[k] = vCopy
			}
		}
	}

	return dst
}

// SerializeDSTState serializes DSTState to JSON.
func SerializeDSTState(state *DSTState) ([]byte, error) {
	return json.Marshal(state)
}

// DeserializeDSTState deserializes DSTState from JSON.
func DeserializeDSTState(data []byte) (*DSTState, error) {
	var state DSTState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

// WriteDSTState writes DSTState to a writer.
func WriteDSTState(w io.Writer, state *DSTState) error {
	data, err := SerializeDSTState(state)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

// ReadDSTState reads DSTState from a reader.
func ReadDSTState(r io.Reader) (*DSTState, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return DeserializeDSTState(data)
}

// SnapshotTreeListener is notified of snapshot tree events.
type SnapshotTreeListener interface {
	OnSnapshotCreated(id SnapshotID, state *DSTState)
	OnSnapshotRestored(id SnapshotID)
	OnSnapshotDeleted(id SnapshotID)
}

// snapshotTreeListeners holds registered listeners.
var snapshotTreeListeners struct {
	mu        sync.RWMutex
	listeners []SnapshotTreeListener
}

// AddSnapshotTreeListener adds a listener for snapshot tree events.
func AddSnapshotTreeListener(l SnapshotTreeListener) {
	snapshotTreeListeners.mu.Lock()
	defer snapshotTreeListeners.mu.Unlock()
	snapshotTreeListeners.listeners = append(snapshotTreeListeners.listeners, l)
}

// notifySnapshotCreated notifies listeners of snapshot creation.
func notifySnapshotCreated(id SnapshotID, state *DSTState) {
	snapshotTreeListeners.mu.RLock()
	defer snapshotTreeListeners.mu.RUnlock()
	for _, l := range snapshotTreeListeners.listeners {
		l.OnSnapshotCreated(id, state)
	}
}

// notifySnapshotRestored notifies listeners of snapshot restoration.
func notifySnapshotRestored(id SnapshotID) {
	snapshotTreeListeners.mu.RLock()
	defer snapshotTreeListeners.mu.RUnlock()
	for _, l := range snapshotTreeListeners.listeners {
		l.OnSnapshotRestored(id)
	}
}

// notifySnapshotDeleted notifies listeners of snapshot deletion.
func notifySnapshotDeleted(id SnapshotID) {
	snapshotTreeListeners.mu.RLock()
	defer snapshotTreeListeners.mu.RUnlock()
	for _, l := range snapshotTreeListeners.listeners {
		l.OnSnapshotDeleted(id)
	}
}
