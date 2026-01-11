# Deterministic Simulation Testing (DST) for gVisor

This fork adds support for deterministic simulation testing, enabling reproducible
execution where the same seed always produces identical results.

## Phase 1: Virtual Time (Complete)

### Files Added

```
pkg/sentry/time/
├── virtual_clocks.go       # VirtualClocks implementation
├── virtual_clocks_test.go  # Tests for VirtualClocks
└── dst.go                  # DST integration helpers

runsc/config/
└── dst.go                  # DST configuration flags

patches/
└── 001-dst-clocks.patch    # Integration patch for loader.go
```

### VirtualClocks API

```go
// Create virtual clocks
cfg := time.VirtualClocksConfig{
    InitialRealtime:  0,     // Unix timestamp in nanoseconds
    InitialMonotonic: 0,     // Monotonic start time
    Frequency:        1_000_000_000, // 1 GHz
}
vc := time.NewVirtualClocks(cfg)

// Get current time (deterministic)
mono, _ := vc.GetTime(time.Monotonic)
real, _ := vc.GetTime(time.Realtime)

// Advance time (this is how time progresses in DST mode)
vc.Advance(1_000_000) // Advance by 1 millisecond

// Checkpoint/restore support
mono, real, cycles := vc.GetState()
vc.SetState(mono, real, cycles)

// Time advancement notifications
vc.AddListener(myListener)
```

### Integration with Timekeeper

```go
// In loader.go, replace:
tk.SetClocks(time.NewCalibratedClocks(), params)

// With:
dstConfig := time.DSTClocksConfig{
    Enabled:          true,
    InitialRealtime:  0,
    InitialMonotonic: 0,
}
clocks := time.NewClocks(dstConfig)
tk.SetClocks(clocks, params)

// Get VirtualClocks for time control
vc := time.GetVirtualClocks(clocks)
```

## Key Properties

1. **Deterministic**: Same operations produce same time values
2. **Controllable**: Time only advances via explicit `Advance()` calls
3. **Checkpoint-friendly**: Full state can be saved/restored
4. **VDSO compatible**: Generates valid timing parameters

## Testing

The tests verify:
- Basic time operations
- Time advancement
- Checkpoint/restore
- Determinism (multiple runs produce identical results)
- Listener notifications

## Phase 2: Deterministic RNG (Complete)

### Files Added

```
pkg/rand/
├── deterministic.go       # ChaCha20-based DeterministicReader
├── deterministic_test.go  # Comprehensive tests (15 test cases)
└── dst.go                 # DST mode enable/disable helpers

patches/
└── 002-dst-rand.patch     # Integration guide
```

### DeterministicReader API

```go
// Create deterministic reader with seed
dr := rand.NewDeterministicReader(12345)

// Read random bytes (deterministic given same seed)
buf := make([]byte, 32)
dr.Read(buf)

// Reseed (reset to initial state with new seed)
dr.Seed(54321)

// Checkpoint/restore support
state, counter, buffer := dr.GetState()
dr.SetState(state, counter, buffer)
```

### DST Mode Integration

```go
// Enable DST mode globally (replaces rand.Reader)
rand.EnableDST(12345)

// All random sources now deterministic:
// - getrandom() syscall
// - /dev/random, /dev/urandom
// - rand.Read() calls

// Check if DST is enabled
if rand.IsDSTEnabled() {
    // Get deterministic reader for state management
    dr := rand.GetDeterministicReader()
}

// Checkpoint RNG state
rngState := rand.GetDSTState()

// Restore RNG state
rand.RestoreDSTState(rngState)

// Reseed for forking execution paths
rand.Reseed(newSeed)

// Disable DST mode
rand.DisableDST()
```

### Key Properties

1. **ChaCha20-based**: Industry-standard PRNG algorithm
2. **Deterministic**: Same seed = identical random sequence
3. **Thread-safe**: Mutex-protected for concurrent access
4. **Checkpoint-friendly**: Full state serialization
5. **Global replacement**: All randomness goes through DST reader

### Test Coverage

- Basic random generation
- Determinism verification
- Different seeds produce different output
- Sequential vs single read equivalence
- Reseed functionality
- Checkpoint/restore
- Large and small reads
- DST enable/disable
- DST state management

## Phase 3: Cooperative Scheduling (Complete)

### Files Added

```
pkg/sentry/kernel/
├── dst_scheduler.go       # DSTScheduler implementation
├── dst_scheduler_test.go  # Comprehensive tests (15+ test cases)
└── dst.go                 # DST scheduling integration helpers

patches/
└── 003-dst-scheduler.patch  # Integration guide
```

### DSTScheduler API

```go
// Create and enable deterministic scheduler
kernel.EnableDSTScheduling(kernel.DSTConfig{
    Enabled: true,
    Seed:    12345,
})

// Get scheduler instance
scheduler := kernel.GetDSTScheduler()

// Register a task with the scheduler
scheduler.RegisterTask(tid)

// Wait for permission to run (blocks until scheduled)
scheduler.WaitForPermit(tid)

// Yield to allow other tasks to run
scheduler.Yield(tid)

// Mark task as blocked (removes from ready queue)
scheduler.Block(tid)

// Mark task as ready again
scheduler.Unblock(tid)

// Unregister task when exiting
scheduler.UnregisterTask(tid)
```

### Checkpoint/Restore

```go
// Save scheduler state
state := scheduler.GetState()

// Restore scheduler state
scheduler.SetState(state)

// Or use kernel helpers
kernelState := kernel.GetDSTKernelState()
kernel.RestoreDSTKernelState(kernelState)
```

### Scheduling Algorithm

The scheduler uses **TID-based ordering** for determinism:
1. Tasks are scheduled in order of lowest TID first
2. Each task yields after completing a syscall
3. Blocked tasks are removed from the ready queue
4. When a task unblocks, it's added back (sorted by TID)

This ensures that given the same initial state:
- Tasks always execute in the same order
- Interleaving is reproducible across runs

### Integration Points

The scheduler integrates at key points in the task lifecycle:
1. **Task Start**: Register task, wait for permit
2. **Syscall Exit**: Yield after each syscall
3. **Task Block**: Remove from ready queue
4. **Task Unblock**: Add back to ready queue
5. **Task Exit**: Unregister task

### Listener Interface

```go
type DSTSchedulerListener interface {
    OnTaskScheduled(tid ThreadID)
    OnTaskYielded(tid ThreadID)
    OnTaskBlocked(tid ThreadID)
    OnTaskUnblocked(tid ThreadID)
}

// Add listener for debugging/tracing
scheduler.AddListener(myListener)
```

### Key Properties

1. **Deterministic**: Same seed = identical task ordering
2. **Cooperative**: Tasks explicitly yield control
3. **TID-ordered**: Lowest TID always runs first when multiple ready
4. **Checkpoint-friendly**: Full state can be saved/restored
5. **Thread-safe**: Mutex-protected internal state

### Test Coverage

- Basic enable/disable
- Single task scheduling
- Multiple task ordering (TID-based)
- Determinism verification (multiple runs identical)
- Block/unblock behavior
- Checkpoint/restore
- Concurrent task execution
- Listener notifications
- Yield counter tracking
- Performance benchmarks

## Phase 4: Deterministic Network (Complete)

### Files Added

```
pkg/tcpip/link/dst/
├── endpoint.go       # DeterministicEndpoint and Network implementation
├── endpoint_test.go  # Comprehensive tests (15+ test cases)
├── dst.go            # DST network integration helpers
└── BUILD             # Bazel build file

patches/
└── 004-dst-network.patch  # Integration guide
```

### Network API

```go
// Enable deterministic networking
dst.EnableDSTNetwork(dst.DSTNetworkConfig{
    Enabled:                  true,
    Seed:                     12345,
    DefaultDelayNS:           1000000, // 1ms delay
    PacketLossProbability:    0.0,
    PacketReorderProbability: 0.0,
})

// Get the deterministic network
network := dst.GetDSTNetwork()

// Create endpoints
ep1 := network.CreateEndpoint(1, 1500, "aa:bb:cc:dd:ee:01")
ep2 := network.CreateEndpoint(2, 1500, "aa:bb:cc:dd:ee:02")

// Connect endpoints
network.Connect(1, 2)

// Attach to stack (ep implements stack.LinkEndpoint)
stack.CreateNIC(1, ep1)
```

### Packet Delivery

```go
// Deliver packets up to virtual time
network.DeliverPendingPackets(virtualTimeNS)

// Or deliver all pending packets
network.DeliverAllPendingPackets()

// Coordinate with virtual time
dst.AdvanceNetworkTime(deltaNS)
```

### Fault Injection

```go
// Inject packet loss (10% probability)
dst.SetPacketLossProbability(0.1)

// Inject packet reordering (5% probability)
dst.SetPacketReorderProbability(0.05)

// Inject network delay (10ms)
dst.SetNetworkDelay(10_000_000)

// Create network partition
dst.CreatePartition(
    []dst.EndpointID{1, 2},    // Set A
    []dst.EndpointID{3, 4, 5}, // Set B
)

// Heal partition
dst.HealPartition(setA, setB)
```

### Checkpoint/Restore

```go
// Save network state
netState := dst.GetDSTNetworkState()

// Restore network state
dst.RestoreDSTNetworkState(netState)
```

### Listener Interface

```go
type NetworkListener interface {
    OnPacketSent(info *PacketInfo)
    OnPacketDelivered(info *PacketInfo)
    OnPacketDropped(info *PacketInfo, reason string)
}

network.AddListener(myListener)
```

### Key Properties

1. **Deterministic ordering**: Packets delivered by (time, ID) order
2. **Virtual delays**: Configurable network latency
3. **Fault injection**: Packet loss, reordering, partitions
4. **Checkpoint-friendly**: Full state serialization
5. **Compatible with netstack**: Implements stack.LinkEndpoint

### Test Coverage

- Basic endpoint creation and connection
- Packet delivery with and without delays
- Determinism verification (multiple runs identical)
- Network disconnect behavior
- Checkpoint/restore
- Packet loss fault injection
- Network listener notifications
- Performance benchmarks

## Phase 5: Deterministic Filesystem (Complete)

### Files Added

```
pkg/sentry/fsimpl/dst/
├── dst.go            # DST filesystem implementation
├── dst_test.go       # Comprehensive tests (15+ test cases)
└── BUILD             # Bazel build file

patches/
└── 005-dst-filesystem.patch  # Integration guide
```

### Filesystem API

```go
// Enable deterministic filesystem mode
fsdst.EnableDSTFilesystem(fsdst.DSTFilesystemConfig{
    Enabled:              true,
    InitialTimeNS:        0,        // Initial filesystem time
    SortDirectoryEntries: true,     // Sort directory listings
    Seed:                 12345,    // Random seed
})

// Check if DST filesystem is enabled
if fsdst.IsDSTFilesystemEnabled() {
    // Get the deterministic clock
    clock := fsdst.GetDSTClock()
}

// Disable DST filesystem mode
fsdst.DisableDSTFilesystem()
```

### Deterministic Clock

```go
// Get filesystem time (deterministic)
timeNS := fsdst.GetFilesystemTime()

// Advance filesystem time
fsdst.AdvanceFilesystemTime(1_000_000) // 1ms

// Set filesystem time
fsdst.SetFilesystemTime(5_000_000_000) // 5 seconds

// Direct clock access
clock := fsdst.GetDSTClock()
ktime := clock.Now()       // Returns ktime.Time
ns := clock.NowNS()        // Returns int64 nanoseconds
clock.Advance(deltaNS)     // Advance by delta
clock.SetTime(absoluteNS)  // Set to absolute time
```

### Deterministic Inode Allocation

```go
// Create inode allocator
alloc := fsdst.NewDeterministicInodeAllocator(1) // Start from inode 1

// Allocate inode numbers sequentially
ino1 := alloc.Allocate() // Returns 1
ino2 := alloc.Allocate() // Returns 2
ino3 := alloc.Allocate() // Returns 3

// Query next inode without allocating
next := alloc.GetNext()

// Set next inode (for checkpoint restore)
alloc.SetNext(100)
```

### Sorted Directory Entries

```go
// Check if sorting is enabled
if fsdst.ShouldSortDirectoryEntries() {
    // Collect entries
    entries := []fsdst.DirectoryEntry{
        {Name: "zebra", Inode: 3, Type: 1},
        {Name: "apple", Inode: 1, Type: 1},
        {Name: "mango", Inode: 2, Type: 1},
    }

    // Sort by name (lexicographic)
    fsdst.SortDirectoryEntries(entries)
    // Result: apple, mango, zebra

    // Or sort by inode
    fsdst.SortDirectoryEntriesByInode(entries)
    // Result: apple (1), mango (2), zebra (3)
}
```

### Checkpoint/Restore

```go
// Save filesystem DST state
state := fsdst.GetDSTFilesystemState()

// Restore filesystem DST state
fsdst.RestoreDSTFilesystemState(state)

// State includes:
// - DSTFilesystemConfig
// - DeterministicClockState (current time)
```

### Filesystem Listeners

```go
type DSTFilesystemListener interface {
    OnFileCreated(path string, inode uint64)
    OnFileDeleted(path string, inode uint64)
    OnFileModified(path string, inode uint64)
    OnDirectoryCreated(path string, inode uint64)
    OnDirectoryDeleted(path string, inode uint64)
}

// Add listener for debugging/tracing
fsdst.AddFilesystemListener(myListener)

// Notify events (called from filesystem operations)
fsdst.NotifyFileCreated("/test/file.txt", 42)
fsdst.NotifyFileModified("/test/file.txt", 42)
fsdst.NotifyFileDeleted("/test/file.txt", 42)
fsdst.NotifyDirectoryCreated("/test/dir", 43)
fsdst.NotifyDirectoryDeleted("/test/dir", 43)
```

### Key Properties

1. **Deterministic Inodes**: Sequential allocation produces same inodes for same operations
2. **Sorted Directories**: Directory listings sorted by name for consistent iteration
3. **Virtual Timestamps**: All timestamps from deterministic clock
4. **Checkpoint-friendly**: Full state serialization for save/restore
5. **Thread-safe**: Mutex-protected internal state

### Test Coverage

- Basic clock operations (Now, Advance, SetTime)
- Clock checkpoint/restore
- Concurrent clock access
- Inode allocation (basic, sequential, concurrent)
- Directory entry sorting (by name, by inode)
- DST enable/disable
- Time operations (Advance, Set, Get)
- ShouldSortDirectoryEntries flag
- Full checkpoint/restore cycle
- Filesystem listener notifications
- Determinism verification (multiple runs identical)
- Performance benchmarks

## Phase 6: Save/Restore for DST (Complete)

### Files Added

```
pkg/sentry/dst/
├── snapshot.go       # DST snapshot implementation
├── snapshot_test.go  # Comprehensive tests (20+ test cases)
└── BUILD             # Bazel build file

patches/
└── 006-dst-snapshot.patch  # Integration guide
```

### Snapshot Tree API

```go
// Create snapshot tree
tree := dst.NewSnapshotTree(maxSnapshots)

// Create snapshot (becomes child of current)
state := &dst.DSTState{
    Metadata: dst.SnapshotMetadata{
        Seed:          12345,
        VirtualTimeNS: 1000000,
        StepCount:     100,
        Description:   "checkpoint-1",
        Tags:          map[string]string{"type": "branch-point"},
    },
    TimeState:       timeState,
    RNGState:        rngState,
    SchedulerState:  schedulerState,
    NetworkState:    networkState,
    FilesystemState: filesystemState,
}
snapshotID, err := tree.CreateSnapshot(state, cowPages)

// Restore to snapshot
restoredState, err := tree.RestoreSnapshot(snapshotID)

// Get snapshot by ID
snapshot, err := tree.GetSnapshot(snapshotID)

// Get current/root snapshot IDs
currentID := tree.GetCurrentID()
rootID := tree.GetRootID()
```

### Snapshot Tree Navigation

```go
// Get children of a snapshot (for branching exploration)
children := tree.GetChildren(parentID)

// Get path from root to snapshot
path, err := tree.GetPath(snapshotID)
// Returns: [rootID, ..., parentID, snapshotID]

// Get depth in tree
depth, err := tree.GetDepth(snapshotID)

// Delete leaf snapshot
err := tree.DeleteSnapshot(snapshotID)

// Prune leaves not on path to current
pruned := tree.PruneLeaves()

// Get snapshot count
count := tree.SnapshotCount()
```

### DSTState Structure

```go
type DSTState struct {
    Metadata         SnapshotMetadata
    TimeState        *VirtualTimeState    // Virtual clock state
    RNGState         *RNGState            // Deterministic RNG state
    SchedulerState   *SchedulerState      // Task scheduling state
    NetworkState     *NetworkState        // Network endpoint/packet state
    FilesystemState  *FilesystemState     // Filesystem clock/inode state
    ApplicationState map[string][]byte    // Custom application state
}

type SnapshotMetadata struct {
    ID            SnapshotID
    ParentID      SnapshotID
    Seed          uint64
    VirtualTimeNS int64
    StepCount     uint64
    Description   string
    Tags          map[string]string
}
```

### Copy-on-Write Page Store

```go
// Create page store
store := dst.NewCoWPageStore()

// Set a page
data := make([]byte, dst.PageSize)
store.SetPage(0x1000, data)

// Get a page (checks parent chain)
data, ok := store.GetPage(0x1000)

// Fork for copy-on-write
child := store.Fork()

// Child sees parent's pages
data, ok := child.GetPage(0x1000) // Found in parent

// Child modifications don't affect parent
child.SetPage(0x1000, modifiedData)
parentData, _ := parent.GetPage(0x1000) // Still original

// Release resources
store.Release()

// Page counts
localPages := store.PageCount()       // Pages in this store only
totalPages := store.TotalPageCount()  // Including parent chain
```

### Serialization

```go
// Serialize state to JSON
data, err := dst.SerializeDSTState(state)

// Deserialize state from JSON
state, err := dst.DeserializeDSTState(data)

// Write state to io.Writer
err := dst.WriteDSTState(writer, state)

// Read state from io.Reader
state, err := dst.ReadDSTState(reader)
```

### Snapshot Listeners

```go
type SnapshotTreeListener interface {
    OnSnapshotCreated(id SnapshotID, state *DSTState)
    OnSnapshotRestored(id SnapshotID)
    OnSnapshotDeleted(id SnapshotID)
}

// Add listener for debugging/tracing
dst.AddSnapshotTreeListener(myListener)
```

### State Space Exploration Example

```go
// Create snapshot tree
tree := dst.NewSnapshotTree(10000)

// Create initial snapshot
initialState := captureDSTState()
rootID, _ := tree.CreateSnapshot(initialState, nil)

// Explore different execution paths
func explore(parentID dst.SnapshotID, depth int) {
    if depth > maxDepth {
        return
    }

    // Restore to parent
    state, _ := tree.RestoreSnapshot(parentID)
    restoreDSTState(state)

    // Run simulation with different seeds
    for variant := 0; variant < 3; variant++ {
        // Restore again for clean state
        tree.RestoreSnapshot(parentID)
        restoreDSTState(state)

        // Modify seed for different path
        rand.Reseed(state.Metadata.Seed + uint64(variant))

        // Run simulation steps
        for i := 0; i < stepsPerBranch; i++ {
            simulationStep()
            if shouldInjectFault() {
                injectFault()
            }
        }

        // Snapshot this variant
        variantState := captureDSTState()
        variantState.Metadata.Description = fmt.Sprintf("depth-%d-var-%d", depth, variant)
        variantID, _ := tree.CreateSnapshot(variantState, nil)

        // Check invariants
        if !checkInvariants() {
            reportBug(parentID, variantID)
        }

        // Recurse
        explore(variantID, depth+1)
    }
}

explore(rootID, 0)
```

### Key Properties

1. **Tree Structure**: Snapshots form a tree for branching exploration
2. **CoW Memory**: Copy-on-write sharing reduces memory overhead
3. **Unified State**: Combines all DST component states
4. **Serializable**: Full state can be saved/loaded from disk
5. **Thread-safe**: Mutex-protected for concurrent access
6. **Configurable Limits**: Max snapshot count to prevent memory exhaustion

### Test Coverage

- CoW page store (set, get, fork, copy-on-write)
- Snapshot tree creation and navigation
- Branching from arbitrary snapshots
- Path computation and depth tracking
- Snapshot deletion and pruning
- Max snapshot limit enforcement
- State serialization/deserialization
- Deep copy verification (modifications don't propagate)
- Concurrent access safety
- Listener notifications
- Performance benchmarks

## Phase 7: Bloodhound Integration (Complete)

### Files Added

```
pkg/sentry/dst/
├── bloodhound.go       # Bloodhound integration (fault injection, properties, coordinator)
├── bloodhound_test.go  # Comprehensive tests (30+ test cases)
├── snapshot.go         # Snapshot tree (from Phase 6)
├── snapshot_test.go    # Snapshot tests
└── BUILD               # Bazel build file

patches/
└── 007-bloodhound-integration.patch  # Integration guide
```

### Fault Injection API

```go
// Create fault injector
fi := dst.NewFaultInjector(seed)

// Enable/disable
fi.Enable(true)
fi.IsEnabled()

// Schedule a fault
id := fi.Schedule(triggerTimeNS, dst.Fault{
    Type:     dst.FaultProcessCrash,
    Target:   "node1",
    Duration: 5_000_000_000, // 5 seconds (for partitions)
})

// Cancel scheduled fault
fi.CancelScheduled(id)

// Check what faults should be injected at time
faults := fi.CheckInjection(currentTimeNS)

// Query next scheduled fault time
nextTime, ok := fi.NextScheduledTime(afterTimeNS)

// Check probability-based injection
if fi.ShouldInjectFault(dst.FaultNetworkDrop, "target") {
    // Inject the fault
}
```

### Fault Types

```go
// Network faults
dst.FaultNetworkPartition  // Isolate nodes
dst.FaultNetworkDrop       // Drop packets
dst.FaultNetworkDelay      // Add latency
dst.FaultNetworkCorrupt    // Corrupt packets

// Disk faults
dst.FaultDiskWriteFailure    // Fail writes
dst.FaultDiskReadFailure     // Fail reads
dst.FaultDiskReadCorruption  // Corrupt read data
dst.FaultDiskWriteCorruption // Corrupt written data
dst.FaultDiskSlow            // Add I/O latency

// Process faults
dst.FaultProcessCrash   // Kill process
dst.FaultProcessPause   // SIGSTOP
dst.FaultProcessOOM     // Out of memory
dst.FaultProcessSIGKILL // Force kill

// Memory faults
dst.FaultMemoryCorruption // Corrupt memory
dst.FaultMemoryPressure   // Memory pressure

// Time faults
dst.FaultClockSkew   // Clock drift
dst.FaultClockJump   // Time jump
dst.FaultClockPause  // Freeze time

// Syscall faults
dst.FaultSyscallEINTR  // Interrupted
dst.FaultSyscallEIO    // I/O error
dst.FaultSyscallENOMEM // No memory
dst.FaultSyscallEAGAIN // Try again
```

### Fault Probabilities

```go
// Predefined probability profiles
fi.SetProbabilities(dst.FaultProbabilitiesCalm())     // No faults
fi.SetProbabilities(dst.FaultProbabilitiesModerate()) // Light faults
fi.SetProbabilities(dst.FaultProbabilitiesChaos())    // Heavy faults

// Custom probabilities
fi.SetProbabilities(dst.FaultProbabilities{
    NetworkDrop:      0.02,
    NetworkDelay:     0.05,
    NetworkPartition: 0.001,
    DiskWriteFailure: 0.001,
    ProcessCrash:     0.0001,
})
```

### Property Checking API

```go
// Create property checker
pc := dst.NewPropertyChecker()

// Add equality check property
pc.AddProperty(dst.Property{
    Name:        "status_ok",
    Description: "Status should be OK",
    Kind:        dst.PropertySafety,
    Check:       &dst.EqualsCheck{Key: "status", Expected: "ok"},
})

// Add range check property
pc.AddProperty(dst.Property{
    Name:        "count_valid",
    Description: "Count in valid range",
    Kind:        dst.PropertyInvariant,
    Check:       &dst.RangeCheck{Key: "count", Min: 0, Max: 100},
})

// Add custom check property
pc.AddProperty(dst.Property{
    Name:        "custom",
    Description: "Custom invariant",
    Kind:        dst.PropertySafety,
    Check: &dst.CustomCheck{
        CheckFn: func(state *dst.SystemState) dst.CheckResult {
            if isConsistent(state) {
                return dst.CheckResult{Status: dst.CheckPass}
            }
            return dst.CheckResult{
                Status: dst.CheckFail,
                Reason: "inconsistent state",
            }
        },
    },
})

// Check all properties
results := pc.CheckAll(systemState)

// Get failures
failures := pc.GetFailures()
```

### Property Kinds

```go
dst.PropertySafety    // Something bad never happens
dst.PropertyLiveness  // Something good eventually happens
dst.PropertyInvariant // Always true
```

### Simulation Coordinator

```go
// Create coordinator
config := dst.SimulationConfig{
    Seed:                  12345,
    MaxSteps:              1000000,
    MaxTimeNS:             60_000_000_000, // 60 seconds
    FaultProbabilities:    dst.FaultProbabilitiesModerate(),
    CheckPropertiesEveryN: 100,
    SnapshotEveryN:        1000,
    StopOnPropertyFailure: true,
    MaxSnapshots:          10000,
}
coord := dst.NewSimulationCoordinator(config)

// Access components
fi := coord.GetFaultInjector()
pc := coord.GetPropertyChecker()
tree := coord.GetSnapshotTree()

// Start simulation
coord.Start()

// Run simulation steps
for coord.IsRunning() {
    faults, propertyResults, running := coord.Step(deltaTimeNS)

    // Handle injected faults
    for _, fault := range faults {
        applyFault(fault)
    }

    // Handle property failures
    for name, result := range propertyResults {
        if result.IsFail() {
            log.Printf("Property %s failed: %s", name, result.Reason)
        }
    }
}

// Stop simulation
coord.Stop("reason")

// Restore to previous snapshot
coord.RestoreToSnapshot(snapshotID)
```

### Simulation Listener

```go
type SimulationListener interface {
    OnSimulationStart(config SimulationConfig)
    OnSimulationStep(step uint64, timeNS int64)
    OnFaultInjected(fault Fault)
    OnPropertyChecked(name string, result CheckResult)
    OnSnapshotCreated(id SnapshotID)
    OnSimulationEnd(reason string)
}

// Add listener
coord.AddListener(myListener)
```

### Bloodhound Event Types

```go
dst.EventSimulationStart   // Simulation started
dst.EventSimulationEnd     // Simulation ended
dst.EventSimulationStep    // Step completed
dst.EventFaultInjected     // Fault was injected
dst.EventFaultScheduled    // Fault was scheduled
dst.EventPropertyChecked   // Property was checked
dst.EventPropertyFailed    // Property check failed
dst.EventSnapshotCreated   // Snapshot was created
dst.EventSnapshotRestored  // Snapshot was restored

// Event serialization
data, _ := dst.SerializeEvent(event)
event, _ := dst.DeserializeEvent(data)
```

### Global Coordinator

```go
// Initialize global coordinator
dst.InitGlobalCoordinator(config)

// Get global coordinator
coord := dst.GetGlobalCoordinator()

// Convenience functions
if dst.GlobalFaultCheck(dst.FaultNetworkDrop, target) {
    // Drop packet
}

id := dst.GlobalScheduleFault(timeNS, fault)
```

### Checkpoint/Restore

```go
// Fault injector state
fiState := fi.GetState()
fi.SetState(fiState)

// Coordinator state
coordState := coord.GetState()
coord.SetState(coordState)
```

### Key Properties

1. **Deterministic Fault Injection**: Same seed = same fault sequence
2. **Scheduled and Random Faults**: Both time-based and probability-based
3. **Property Checking**: Safety, liveness, and invariant properties
4. **Simulation Coordination**: Orchestrates all DST components
5. **Event Streaming**: Full event log for debugging and replay
6. **Checkpoint/Restore**: Full state serialization

### Test Coverage

- Fault injector basic operations
- Scheduled fault triggering
- Multiple fault scheduling and ordering
- Fault cancellation
- Partition heal timing
- Probability-based injection
- Determinism verification
- Statistics tracking
- Checkpoint/restore
- Property checker (equals, range, custom)
- Property failures tracking
- Simulation coordinator lifecycle
- Fault injection during simulation
- Property failure stopping simulation
- Snapshot creation during simulation
- Simulation listener events
- Snapshot restore during simulation
- Event serialization
- Global coordinator
- Performance benchmarks

## All Phases Complete

All 7 phases of DST support for gVisor are now complete:

| Phase | Component | Status |
|-------|-----------|--------|
| 1 | Virtual Time | Complete |
| 2 | Deterministic RNG | Complete |
| 3 | Cooperative Scheduling | Complete |
| 4 | Deterministic Network | Complete |
| 5 | Deterministic Filesystem | Complete |
| 6 | Save/Restore for DST | Complete |
| 7 | Bloodhound Integration | Complete |

## Building

gVisor uses Bazel:

```bash
# Build runsc with DST support
bazel build //runsc:runsc

# Run tests
bazel test //pkg/sentry/time:time_test
```

## Usage with Bloodhound

```bash
# Start container in DST mode (once fully integrated)
runsc --dst --dst-seed=12345 run mycontainer
```

---

*Phase 1 (Virtual Time) implemented: January 2026*
*Phase 2 (Deterministic RNG) implemented: January 2026*
*Phase 3 (Cooperative Scheduling) implemented: January 2026*
*Phase 4 (Deterministic Network) implemented: January 2026*
*Phase 5 (Deterministic Filesystem) implemented: January 2026*
*Phase 6 (Save/Restore for DST) implemented: January 2026*
*Phase 7 (Bloodhound Integration) implemented: January 2026*
