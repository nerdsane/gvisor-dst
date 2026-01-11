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
	"bytes"
	"sync"
	"testing"
)

func TestCoWPageStoreBasic(t *testing.T) {
	store := NewCoWPageStore()
	defer store.Release()

	// Set a page
	data := make([]byte, PageSize)
	for i := range data {
		data[i] = byte(i % 256)
	}
	store.SetPage(0x1000, data)

	// Get the page back
	got, ok := store.GetPage(0x1000)
	if !ok {
		t.Fatal("GetPage failed for set page")
	}
	if !bytes.Equal(got, data) {
		t.Error("Retrieved page data doesn't match")
	}

	// Get non-existent page
	_, ok = store.GetPage(0x2000)
	if ok {
		t.Error("GetPage should fail for non-existent page")
	}
}

func TestCoWPageStoreFork(t *testing.T) {
	parent := NewCoWPageStore()
	defer parent.Release()

	// Set page in parent
	parentData := []byte("parent page data")
	parent.SetPage(0x1000, parentData)

	// Fork child
	child := parent.Fork()
	defer child.Release()

	// Child should see parent's page
	got, ok := child.GetPage(0x1000)
	if !ok {
		t.Fatal("Child should see parent's page")
	}
	if !bytes.Equal(got, parentData) {
		t.Error("Child page data doesn't match parent")
	}

	// Write new page in child
	childData := []byte("child page data")
	child.SetPage(0x2000, childData)

	// Parent should not see child's page
	_, ok = parent.GetPage(0x2000)
	if ok {
		t.Error("Parent should not see child's page")
	}

	// Child should see its own page
	got, ok = child.GetPage(0x2000)
	if !ok {
		t.Fatal("Child should see its own page")
	}
	if !bytes.Equal(got, childData) {
		t.Error("Child's own page data doesn't match")
	}
}

func TestCoWPageStoreCopyOnWrite(t *testing.T) {
	parent := NewCoWPageStore()
	defer parent.Release()

	// Set page in parent
	originalData := []byte("original data")
	parent.SetPage(0x1000, originalData)

	// Fork child
	child := parent.Fork()
	defer child.Release()

	// Overwrite page in child
	modifiedData := []byte("modified data")
	child.SetPage(0x1000, modifiedData)

	// Parent should still see original
	got, _ := parent.GetPage(0x1000)
	if !bytes.Equal(got, originalData) {
		t.Error("Parent's page was modified by child")
	}

	// Child should see modified
	got, _ = child.GetPage(0x1000)
	if !bytes.Equal(got, modifiedData) {
		t.Error("Child should see modified page")
	}
}

func TestCoWPageStorePageCount(t *testing.T) {
	parent := NewCoWPageStore()
	defer parent.Release()

	parent.SetPage(0x1000, []byte("page1"))
	parent.SetPage(0x2000, []byte("page2"))

	if parent.PageCount() != 2 {
		t.Errorf("PageCount = %d, want 2", parent.PageCount())
	}

	child := parent.Fork()
	defer child.Release()

	// Child has no local pages yet
	if child.PageCount() != 0 {
		t.Errorf("Child PageCount = %d, want 0", child.PageCount())
	}

	child.SetPage(0x3000, []byte("page3"))
	if child.PageCount() != 1 {
		t.Errorf("Child PageCount after set = %d, want 1", child.PageCount())
	}
}

func TestSnapshotTreeBasic(t *testing.T) {
	tree := NewSnapshotTree(100)

	// Create root snapshot
	state := &DSTState{
		Metadata: SnapshotMetadata{
			Seed:          12345,
			VirtualTimeNS: 0,
			StepCount:     0,
			Description:   "root snapshot",
		},
		TimeState: &VirtualTimeState{
			MonotonicNS: 0,
			RealtimeNS:  0,
		},
	}

	id, err := tree.CreateSnapshot(state, nil)
	if err != nil {
		t.Fatalf("CreateSnapshot failed: %v", err)
	}

	if id == InvalidSnapshotID {
		t.Error("Got invalid snapshot ID")
	}

	if tree.SnapshotCount() != 1 {
		t.Errorf("SnapshotCount = %d, want 1", tree.SnapshotCount())
	}

	if tree.GetRootID() != id {
		t.Errorf("GetRootID = %d, want %d", tree.GetRootID(), id)
	}

	if tree.GetCurrentID() != id {
		t.Errorf("GetCurrentID = %d, want %d", tree.GetCurrentID(), id)
	}
}

func TestSnapshotTreeBranching(t *testing.T) {
	tree := NewSnapshotTree(100)

	// Create root
	rootState := &DSTState{
		Metadata: SnapshotMetadata{Description: "root"},
		TimeState: &VirtualTimeState{MonotonicNS: 0},
	}
	rootID, _ := tree.CreateSnapshot(rootState, nil)

	// Create child1
	child1State := &DSTState{
		Metadata: SnapshotMetadata{Description: "child1"},
		TimeState: &VirtualTimeState{MonotonicNS: 1000},
	}
	child1ID, _ := tree.CreateSnapshot(child1State, nil)

	// Restore to root to branch
	_, err := tree.RestoreSnapshot(rootID)
	if err != nil {
		t.Fatalf("RestoreSnapshot failed: %v", err)
	}

	// Create child2 (branching from root)
	child2State := &DSTState{
		Metadata: SnapshotMetadata{Description: "child2"},
		TimeState: &VirtualTimeState{MonotonicNS: 2000},
	}
	child2ID, _ := tree.CreateSnapshot(child2State, nil)

	// Verify tree structure
	children := tree.GetChildren(rootID)
	if len(children) != 2 {
		t.Errorf("Root has %d children, want 2", len(children))
	}

	// Verify children are correct
	hasChild1, hasChild2 := false, false
	for _, c := range children {
		if c == child1ID {
			hasChild1 = true
		}
		if c == child2ID {
			hasChild2 = true
		}
	}
	if !hasChild1 || !hasChild2 {
		t.Error("Children IDs don't match expected")
	}
}

func TestSnapshotTreePath(t *testing.T) {
	tree := NewSnapshotTree(100)

	// Create a chain: root -> child -> grandchild
	rootState := &DSTState{Metadata: SnapshotMetadata{Description: "root"}}
	rootID, _ := tree.CreateSnapshot(rootState, nil)

	childState := &DSTState{Metadata: SnapshotMetadata{Description: "child"}}
	childID, _ := tree.CreateSnapshot(childState, nil)

	grandchildState := &DSTState{Metadata: SnapshotMetadata{Description: "grandchild"}}
	grandchildID, _ := tree.CreateSnapshot(grandchildState, nil)

	// Get path to grandchild
	path, err := tree.GetPath(grandchildID)
	if err != nil {
		t.Fatalf("GetPath failed: %v", err)
	}

	if len(path) != 3 {
		t.Errorf("Path length = %d, want 3", len(path))
	}

	if path[0] != rootID || path[1] != childID || path[2] != grandchildID {
		t.Errorf("Path = %v, want [%d, %d, %d]", path, rootID, childID, grandchildID)
	}

	// Check depth
	depth, _ := tree.GetDepth(grandchildID)
	if depth != 3 {
		t.Errorf("Depth = %d, want 3", depth)
	}
}

func TestSnapshotTreeDelete(t *testing.T) {
	tree := NewSnapshotTree(100)

	// Create root -> child
	rootState := &DSTState{Metadata: SnapshotMetadata{Description: "root"}}
	rootID, _ := tree.CreateSnapshot(rootState, nil)

	childState := &DSTState{Metadata: SnapshotMetadata{Description: "child"}}
	childID, _ := tree.CreateSnapshot(childState, nil)

	// Cannot delete root (has child)
	err := tree.DeleteSnapshot(rootID)
	if err == nil {
		t.Error("Should not be able to delete snapshot with children")
	}

	// Can delete child (leaf)
	err = tree.DeleteSnapshot(childID)
	if err != nil {
		t.Fatalf("DeleteSnapshot failed: %v", err)
	}

	if tree.SnapshotCount() != 1 {
		t.Errorf("SnapshotCount = %d, want 1", tree.SnapshotCount())
	}

	// Current should move to parent
	if tree.GetCurrentID() != rootID {
		t.Errorf("CurrentID = %d, want %d", tree.GetCurrentID(), rootID)
	}
}

func TestSnapshotTreePruneLeaves(t *testing.T) {
	tree := NewSnapshotTree(100)

	// Create: root -> a -> b (current)
	//              -> c -> d (leaf to prune)
	//              -> e (leaf to prune)
	rootState := &DSTState{Metadata: SnapshotMetadata{Description: "root"}}
	rootID, _ := tree.CreateSnapshot(rootState, nil)

	aState := &DSTState{Metadata: SnapshotMetadata{Description: "a"}}
	aID, _ := tree.CreateSnapshot(aState, nil)

	bState := &DSTState{Metadata: SnapshotMetadata{Description: "b"}}
	bID, _ := tree.CreateSnapshot(bState, nil)

	// Branch from root for c
	tree.RestoreSnapshot(rootID)
	cState := &DSTState{Metadata: SnapshotMetadata{Description: "c"}}
	tree.CreateSnapshot(cState, nil)

	dState := &DSTState{Metadata: SnapshotMetadata{Description: "d"}}
	tree.CreateSnapshot(dState, nil)

	// Branch from root for e
	tree.RestoreSnapshot(rootID)
	eState := &DSTState{Metadata: SnapshotMetadata{Description: "e"}}
	tree.CreateSnapshot(eState, nil)

	// Restore to b (make it current)
	tree.RestoreSnapshot(bID)

	// Should have 6 snapshots
	if tree.SnapshotCount() != 6 {
		t.Errorf("Before prune: SnapshotCount = %d, want 6", tree.SnapshotCount())
	}

	// Prune leaves not on path to current
	pruned := tree.PruneLeaves()
	if pruned != 2 {
		t.Errorf("Pruned %d leaves, want 2 (d and e)", pruned)
	}

	// Should have 4 snapshots: root, a, b, c
	if tree.SnapshotCount() != 4 {
		t.Errorf("After prune: SnapshotCount = %d, want 4", tree.SnapshotCount())
	}

	// Current should still be b
	if tree.GetCurrentID() != bID {
		t.Errorf("CurrentID = %d, want %d", tree.GetCurrentID(), bID)
	}

	// Path to b should still work
	path, _ := tree.GetPath(bID)
	if len(path) != 3 {
		t.Errorf("Path to b has %d elements, want 3", len(path))
	}
	if path[0] != rootID || path[1] != aID || path[2] != bID {
		t.Error("Path to b is incorrect")
	}
}

func TestSnapshotTreeMaxSnapshots(t *testing.T) {
	tree := NewSnapshotTree(5)

	// Create 5 snapshots (at limit)
	for i := 0; i < 5; i++ {
		state := &DSTState{Metadata: SnapshotMetadata{StepCount: uint64(i)}}
		_, err := tree.CreateSnapshot(state, nil)
		if err != nil {
			t.Fatalf("CreateSnapshot %d failed: %v", i, err)
		}
	}

	// 6th should fail
	state := &DSTState{Metadata: SnapshotMetadata{StepCount: 5}}
	_, err := tree.CreateSnapshot(state, nil)
	if err == nil {
		t.Error("Should fail when snapshot limit reached")
	}
}

func TestSnapshotTreeRestore(t *testing.T) {
	tree := NewSnapshotTree(100)

	// Create snapshot with specific state
	originalState := &DSTState{
		Metadata: SnapshotMetadata{
			Seed:          12345,
			VirtualTimeNS: 1000,
			StepCount:     100,
			Description:   "test",
			Tags:          map[string]string{"key": "value"},
		},
		TimeState: &VirtualTimeState{
			MonotonicNS: 1000,
			RealtimeNS:  2000,
			Cycles:      3000,
		},
		RNGState: &RNGState{
			Counter: 42,
			BufPos:  10,
		},
	}

	id, _ := tree.CreateSnapshot(originalState, nil)

	// Restore and verify
	restored, err := tree.RestoreSnapshot(id)
	if err != nil {
		t.Fatalf("RestoreSnapshot failed: %v", err)
	}

	// Verify metadata
	if restored.Metadata.Seed != 12345 {
		t.Errorf("Seed = %d, want 12345", restored.Metadata.Seed)
	}
	if restored.Metadata.VirtualTimeNS != 1000 {
		t.Errorf("VirtualTimeNS = %d, want 1000", restored.Metadata.VirtualTimeNS)
	}
	if restored.Metadata.Tags["key"] != "value" {
		t.Error("Tags not restored correctly")
	}

	// Verify time state
	if restored.TimeState.MonotonicNS != 1000 {
		t.Errorf("MonotonicNS = %d, want 1000", restored.TimeState.MonotonicNS)
	}

	// Verify RNG state
	if restored.RNGState.Counter != 42 {
		t.Errorf("RNG Counter = %d, want 42", restored.RNGState.Counter)
	}

	// Modifying restored should not affect original
	restored.Metadata.Seed = 99999
	snapshot, _ := tree.GetSnapshot(id)
	if snapshot.State.Metadata.Seed != 12345 {
		t.Error("Modification of restored state affected original")
	}
}

func TestDSTStateSerialization(t *testing.T) {
	original := &DSTState{
		Metadata: SnapshotMetadata{
			ID:            1,
			ParentID:      0,
			Seed:          12345,
			VirtualTimeNS: 1000000,
			StepCount:     500,
			Description:   "test snapshot",
			Tags:          map[string]string{"env": "test", "version": "1.0"},
		},
		TimeState: &VirtualTimeState{
			MonotonicNS: 1000000,
			RealtimeNS:  2000000,
			Cycles:      3000000,
		},
		RNGState: &RNGState{
			Counter: 100,
			Buffer:  []byte{1, 2, 3, 4},
			BufPos:  2,
		},
		SchedulerState: &SchedulerState{
			TaskStates: map[int32]TaskState{
				1: {TID: 1, State: "ready", Priority: 10},
				2: {TID: 2, State: "blocked", Priority: 5},
			},
			ReadyQueue:   []int32{1},
			CurrentTask:  1,
			YieldCounter: 50,
		},
		NetworkState: &NetworkState{
			EndpointStates: map[uint64]EndpointState{
				1: {ID: 1, MTU: 1500, MACAddress: "aa:bb:cc:dd:ee:ff", Connected: []uint64{2}},
			},
			PendingPackets: []PacketState{
				{ID: 1, SrcEndpoint: 1, DstEndpoint: 2, Data: []byte("hello"), DeliveryTime: 1000},
			},
			NextPacketID:  2,
			CurrentTimeNS: 1000,
			Config: NetworkConfig{
				DefaultDelayNS:        1000000,
				PacketLossProbability: 0.01,
			},
		},
		FilesystemState: &FilesystemState{
			ClockTimeNS: 1000000,
			NextInode:   100,
			Config: FilesystemConfig{
				Enabled:              true,
				SortDirectoryEntries: true,
				Seed:                 12345,
			},
		},
		ApplicationState: map[string][]byte{
			"app1": []byte("state1"),
			"app2": []byte("state2"),
		},
	}

	// Serialize
	data, err := SerializeDSTState(original)
	if err != nil {
		t.Fatalf("SerializeDSTState failed: %v", err)
	}

	// Deserialize
	restored, err := DeserializeDSTState(data)
	if err != nil {
		t.Fatalf("DeserializeDSTState failed: %v", err)
	}

	// Verify
	if restored.Metadata.Seed != original.Metadata.Seed {
		t.Error("Seed mismatch")
	}
	if restored.TimeState.MonotonicNS != original.TimeState.MonotonicNS {
		t.Error("TimeState mismatch")
	}
	if restored.RNGState.Counter != original.RNGState.Counter {
		t.Error("RNGState mismatch")
	}
	if len(restored.SchedulerState.TaskStates) != 2 {
		t.Error("SchedulerState task count mismatch")
	}
	if len(restored.NetworkState.PendingPackets) != 1 {
		t.Error("NetworkState packet count mismatch")
	}
	if restored.FilesystemState.NextInode != 100 {
		t.Error("FilesystemState mismatch")
	}
	if string(restored.ApplicationState["app1"]) != "state1" {
		t.Error("ApplicationState mismatch")
	}
}

func TestDSTStateReadWrite(t *testing.T) {
	original := &DSTState{
		Metadata: SnapshotMetadata{
			Seed:        42,
			Description: "io test",
		},
		TimeState: &VirtualTimeState{MonotonicNS: 1000},
	}

	// Write to buffer
	var buf bytes.Buffer
	err := WriteDSTState(&buf, original)
	if err != nil {
		t.Fatalf("WriteDSTState failed: %v", err)
	}

	// Read back
	restored, err := ReadDSTState(&buf)
	if err != nil {
		t.Fatalf("ReadDSTState failed: %v", err)
	}

	if restored.Metadata.Seed != 42 {
		t.Errorf("Seed = %d, want 42", restored.Metadata.Seed)
	}
	if restored.Metadata.Description != "io test" {
		t.Error("Description mismatch")
	}
}

func TestCopyDSTState(t *testing.T) {
	original := &DSTState{
		Metadata: SnapshotMetadata{
			Tags: map[string]string{"key": "value"},
		},
		SchedulerState: &SchedulerState{
			TaskStates: map[int32]TaskState{
				1: {TID: 1, State: "ready"},
			},
			ReadyQueue: []int32{1, 2, 3},
		},
	}

	copied := copyDSTState(original)

	// Modify original
	original.Metadata.Tags["key"] = "modified"
	original.SchedulerState.TaskStates[1] = TaskState{TID: 1, State: "blocked"}
	original.SchedulerState.ReadyQueue[0] = 999

	// Copy should be unaffected
	if copied.Metadata.Tags["key"] != "value" {
		t.Error("Copy's tags were modified")
	}
	if copied.SchedulerState.TaskStates[1].State != "ready" {
		t.Error("Copy's task state was modified")
	}
	if copied.SchedulerState.ReadyQueue[0] != 1 {
		t.Error("Copy's ready queue was modified")
	}
}

func TestSnapshotTreeConcurrent(t *testing.T) {
	tree := NewSnapshotTree(1000)

	var wg sync.WaitGroup

	// Create root first
	rootState := &DSTState{Metadata: SnapshotMetadata{Description: "root"}}
	rootID, _ := tree.CreateSnapshot(rootState, nil)

	// Concurrent snapshot creation
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				state := &DSTState{
					Metadata: SnapshotMetadata{
						StepCount:   uint64(idx*10 + j),
						Description: "concurrent",
					},
				}
				tree.RestoreSnapshot(rootID)
				tree.CreateSnapshot(state, nil)
			}
		}(i)
	}

	wg.Wait()

	// Should have created many snapshots without panicking
	if tree.SnapshotCount() < 10 {
		t.Errorf("SnapshotCount = %d, expected more", tree.SnapshotCount())
	}
}

type testSnapshotListener struct {
	mu       sync.Mutex
	created  []SnapshotID
	restored []SnapshotID
	deleted  []SnapshotID
}

func (l *testSnapshotListener) OnSnapshotCreated(id SnapshotID, state *DSTState) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.created = append(l.created, id)
}

func (l *testSnapshotListener) OnSnapshotRestored(id SnapshotID) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.restored = append(l.restored, id)
}

func (l *testSnapshotListener) OnSnapshotDeleted(id SnapshotID) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.deleted = append(l.deleted, id)
}

func TestSnapshotListeners(t *testing.T) {
	listener := &testSnapshotListener{}
	AddSnapshotTreeListener(listener)

	// Notify events
	notifySnapshotCreated(1, &DSTState{})
	notifySnapshotRestored(1)
	notifySnapshotDeleted(1)

	listener.mu.Lock()
	defer listener.mu.Unlock()

	if len(listener.created) != 1 || listener.created[0] != 1 {
		t.Error("OnSnapshotCreated not called correctly")
	}
	if len(listener.restored) != 1 || listener.restored[0] != 1 {
		t.Error("OnSnapshotRestored not called correctly")
	}
	if len(listener.deleted) != 1 || listener.deleted[0] != 1 {
		t.Error("OnSnapshotDeleted not called correctly")
	}
}

func BenchmarkSnapshotCreate(b *testing.B) {
	tree := NewSnapshotTree(b.N + 10)
	state := &DSTState{
		Metadata: SnapshotMetadata{Description: "benchmark"},
		TimeState: &VirtualTimeState{MonotonicNS: 1000},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tree.CreateSnapshot(state, nil)
	}
}

func BenchmarkSnapshotRestore(b *testing.B) {
	tree := NewSnapshotTree(100)
	state := &DSTState{
		Metadata: SnapshotMetadata{Description: "benchmark"},
		TimeState: &VirtualTimeState{MonotonicNS: 1000},
		SchedulerState: &SchedulerState{
			TaskStates: map[int32]TaskState{
				1: {TID: 1, State: "ready"},
				2: {TID: 2, State: "ready"},
			},
			ReadyQueue: []int32{1, 2},
		},
	}
	id, _ := tree.CreateSnapshot(state, nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tree.RestoreSnapshot(id)
	}
}

func BenchmarkCoWPageSet(b *testing.B) {
	store := NewCoWPageStore()
	defer store.Release()

	data := make([]byte, PageSize)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store.SetPage(uint64(i)*PageSize, data)
	}
}

func BenchmarkCoWPageGet(b *testing.B) {
	store := NewCoWPageStore()
	defer store.Release()

	data := make([]byte, PageSize)
	store.SetPage(0x1000, data)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store.GetPage(0x1000)
	}
}

func BenchmarkDSTStateSerialization(b *testing.B) {
	state := &DSTState{
		Metadata: SnapshotMetadata{
			Seed:        12345,
			Description: "benchmark",
		},
		TimeState: &VirtualTimeState{MonotonicNS: 1000},
		SchedulerState: &SchedulerState{
			TaskStates: map[int32]TaskState{
				1: {TID: 1, State: "ready"},
			},
			ReadyQueue: []int32{1},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		SerializeDSTState(state)
	}
}
