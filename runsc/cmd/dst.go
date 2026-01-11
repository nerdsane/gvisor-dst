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

package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/subcommands"
	"gvisor.dev/gvisor/pkg/sentry/dst"
	"gvisor.dev/gvisor/runsc/boot"
	"gvisor.dev/gvisor/runsc/cmd/util"
	"gvisor.dev/gvisor/runsc/config"
	"gvisor.dev/gvisor/runsc/container"
	"gvisor.dev/gvisor/runsc/flag"
)

// DST implements subcommands.Command for the "dst" command.
type DST struct {
	// action is the DST action to perform.
	action string

	// steps is the number of steps to advance (for "step" action).
	steps uint64

	// deltaNS is the time delta per step in nanoseconds (for "step" action).
	deltaNS int64

	// Fault probabilities (for "set-faults" action).
	faultNetworkDrop      float64
	faultNetworkDelay     float64
	faultNetworkPartition float64
	faultDiskWrite        float64
	faultDiskRead         float64
	faultSyscall          float64

	// Schedule fault parameters (for "schedule-fault" action).
	faultType      string
	faultTarget    string
	faultTriggerNS int64
	faultDuration  int64
}

// Name implements subcommands.Command.
func (*DST) Name() string {
	return "dst"
}

// Synopsis implements subcommands.Command.
func (*DST) Synopsis() string {
	return "control Deterministic Simulation Testing (DST)"
}

// Usage implements subcommands.Command.
func (*DST) Usage() string {
	return `dst [flags] <container id>

Interact with a DST-enabled sandbox. Actions:
  state          - Get current DST state
  step           - Advance simulation by steps
  pause          - Pause the simulation
  resume         - Resume the simulation
  stats          - Get fault injection statistics
  set-faults     - Set fault injection probabilities
  schedule-fault - Schedule a fault for injection at a specific time
`
}

// SetFlags implements subcommands.Command.
func (d *DST) SetFlags(f *flag.FlagSet) {
	f.StringVar(&d.action, "action", "state", "DST action: state, step, pause, resume, stats, set-faults, schedule-fault")
	f.Uint64Var(&d.steps, "steps", 1, "Number of steps to advance (for step action)")
	f.Int64Var(&d.deltaNS, "delta-ns", 1000000, "Time delta per step in nanoseconds (for step action)")

	// Fault probability flags (for set-faults action)
	f.Float64Var(&d.faultNetworkDrop, "fault-network-drop", 0, "Probability of network packet drop (0.0-1.0)")
	f.Float64Var(&d.faultNetworkDelay, "fault-network-delay", 0, "Probability of network delay (0.0-1.0)")
	f.Float64Var(&d.faultNetworkPartition, "fault-network-partition", 0, "Probability of network partition (0.0-1.0)")
	f.Float64Var(&d.faultDiskWrite, "fault-disk-write", 0, "Probability of disk write failure (0.0-1.0)")
	f.Float64Var(&d.faultDiskRead, "fault-disk-read", 0, "Probability of disk read failure (0.0-1.0)")
	f.Float64Var(&d.faultSyscall, "fault-syscall", 0, "Probability of syscall failure (0.0-1.0)")

	// Schedule fault flags (for schedule-fault action)
	f.StringVar(&d.faultType, "fault-type", "network_drop", "Type of fault: network_drop, network_delay, network_partition, disk_write_failure, disk_read_failure, syscall_eintr")
	f.StringVar(&d.faultTarget, "fault-target", "", "Target for the fault (e.g., process name, endpoint)")
	f.Int64Var(&d.faultTriggerNS, "fault-trigger-ns", 0, "Virtual time in NS when fault should trigger (0 = current time + 1ms)")
	f.Int64Var(&d.faultDuration, "fault-duration-ns", 0, "Duration of the fault in nanoseconds (0 = instant)")
}

// Execute implements subcommands.Command.Execute.
func (d *DST) Execute(_ context.Context, f *flag.FlagSet, args ...any) subcommands.ExitStatus {
	if f.NArg() != 1 {
		f.Usage()
		return subcommands.ExitUsageError
	}

	conf := args[0].(*config.Config)
	id := f.Arg(0)

	c, err := container.Load(conf.RootDir, container.FullID{ContainerID: id}, container.LoadOpts{SkipCheck: true})
	if err != nil {
		return util.Errorf("loading container %q: %v", id, err)
	}

	if !c.IsSandboxRunning() {
		return util.Errorf("container sandbox is not running")
	}

	switch d.action {
	case "state":
		return d.getState(c)
	case "step":
		return d.step(c)
	case "pause":
		return d.pause(c)
	case "resume":
		return d.resume(c)
	case "stats":
		return d.getStats(c)
	case "set-faults":
		return d.setFaults(c)
	case "schedule-fault":
		return d.scheduleFault(c)
	default:
		return util.Errorf("unknown action: %s", d.action)
	}
}

func (d *DST) getState(c *container.Container) subcommands.ExitStatus {
	state, err := c.Sandbox.DSTGetState()
	if err != nil {
		return util.Errorf("getting DST state: %v", err)
	}

	out, _ := json.MarshalIndent(state, "", "  ")
	fmt.Println(string(out))
	return subcommands.ExitSuccess
}

func (d *DST) step(c *container.Container) subcommands.ExitStatus {
	result, err := c.Sandbox.DSTStep(d.steps, d.deltaNS)
	if err != nil {
		return util.Errorf("stepping DST: %v", err)
	}

	out, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(out))
	return subcommands.ExitSuccess
}

func (d *DST) pause(c *container.Container) subcommands.ExitStatus {
	if err := c.Sandbox.DSTPause(); err != nil {
		return util.Errorf("pausing DST: %v", err)
	}
	fmt.Println("DST simulation paused")
	return subcommands.ExitSuccess
}

func (d *DST) resume(c *container.Container) subcommands.ExitStatus {
	if err := c.Sandbox.DSTResume(); err != nil {
		return util.Errorf("resuming DST: %v", err)
	}
	fmt.Println("DST simulation resumed")
	return subcommands.ExitSuccess
}

func (d *DST) getStats(c *container.Container) subcommands.ExitStatus {
	stats, err := c.Sandbox.DSTGetStats()
	if err != nil {
		return util.Errorf("getting DST stats: %v", err)
	}

	out, _ := json.MarshalIndent(stats, "", "  ")
	fmt.Println(string(out))
	return subcommands.ExitSuccess
}

func (d *DST) setFaults(c *container.Container) subcommands.ExitStatus {
	args := &boot.SetFaultProbabilitiesArgs{
		FaultProbabilities: dst.FaultProbabilities{
			NetworkDrop:      d.faultNetworkDrop,
			NetworkDelay:     d.faultNetworkDelay,
			NetworkPartition: d.faultNetworkPartition,
			DiskWriteFailure: d.faultDiskWrite,
			DiskReadFailure:  d.faultDiskRead,
			SyscallFailure:   d.faultSyscall,
		},
	}

	if err := c.Sandbox.DSTSetFaultProbabilities(args); err != nil {
		return util.Errorf("setting fault probabilities: %v", err)
	}

	fmt.Printf("Fault probabilities set:\n")
	fmt.Printf("  Network drop:      %.4f\n", d.faultNetworkDrop)
	fmt.Printf("  Network delay:     %.4f\n", d.faultNetworkDelay)
	fmt.Printf("  Network partition: %.4f\n", d.faultNetworkPartition)
	fmt.Printf("  Disk write:        %.4f\n", d.faultDiskWrite)
	fmt.Printf("  Disk read:         %.4f\n", d.faultDiskRead)
	fmt.Printf("  Syscall:           %.4f\n", d.faultSyscall)
	return subcommands.ExitSuccess
}

func (d *DST) scheduleFault(c *container.Container) subcommands.ExitStatus {
	// Get current state to determine trigger time if not specified
	triggerNS := d.faultTriggerNS
	if triggerNS == 0 {
		state, err := c.Sandbox.DSTGetState()
		if err != nil {
			return util.Errorf("getting DST state: %v", err)
		}
		triggerNS = state.VirtualTimeNS + 1000000 // 1ms in the future
	}

	args := &boot.ScheduleFaultArgs{
		TriggerTimeNS: triggerNS,
		Fault: dst.Fault{
			Type:     dst.FaultType(d.faultType),
			Target:   d.faultTarget,
			Duration: d.faultDuration,
		},
	}

	result, err := c.Sandbox.DSTScheduleFault(args)
	if err != nil {
		return util.Errorf("scheduling fault: %v", err)
	}

	fmt.Printf("Fault scheduled:\n")
	fmt.Printf("  Fault ID:     %d\n", result.FaultID)
	fmt.Printf("  Type:         %s\n", d.faultType)
	fmt.Printf("  Target:       %s\n", d.faultTarget)
	fmt.Printf("  Trigger time: %d ns\n", triggerNS)
	fmt.Printf("  Duration:     %d ns\n", d.faultDuration)
	return subcommands.ExitSuccess
}
