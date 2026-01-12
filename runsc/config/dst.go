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

package config

import (
	"gvisor.dev/gvisor/runsc/flag"
)

// DSTConfig contains configuration for Deterministic Simulation Testing mode.
// When enabled, gVisor uses virtual time and deterministic scheduling to ensure
// reproducible execution.
type DSTConfig struct {
	// Enabled indicates whether DST mode is active.
	Enabled bool

	// Seed is the random seed for deterministic execution.
	// Same seed produces identical execution sequences.
	Seed uint64

	// InitialRealtime is the initial wall clock time (Unix timestamp in nanoseconds).
	// Defaults to 0 (Unix epoch).
	InitialRealtime int64

	// VirtualTimeAdvanceNS is how much virtual time advances per syscall.
	// Defaults to 1000 (1 microsecond).
	VirtualTimeAdvanceNS int64

	// ControlSocket is the path to the Unix socket for Bloodhound control.
	// If empty, no control socket is created.
	ControlSocket string

	// MaxSteps is the maximum number of simulation steps (0 = unlimited).
	MaxSteps uint64

	// MaxTimeNS is the maximum virtual time in nanoseconds (0 = unlimited).
	MaxTimeNS int64

	// FaultNetworkDrop is the probability of dropping network packets (0.0-1.0).
	FaultNetworkDrop float64

	// FaultDiskWrite is the probability of disk write failures (0.0-1.0).
	FaultDiskWrite float64

	// FaultDiskRead is the probability of disk read failures (0.0-1.0).
	FaultDiskRead float64

	// FaultSyscall is the probability of syscall failures (0.0-1.0).
	FaultSyscall float64

	// FaultClockSkew is the probability of injecting clock skew (0.0-1.0).
	// When triggered, the clock will drift by a random amount.
	FaultClockSkew float64

	// FaultClockJump is the probability of injecting clock jumps (0.0-1.0).
	// When triggered, the clock will jump forward or backward by a random amount.
	FaultClockJump float64

	// ClockSkewMaxNS is the maximum clock skew to inject in nanoseconds.
	// Defaults to 1 second (1,000,000,000 ns).
	ClockSkewMaxNS int64

	// ClockJumpMaxNS is the maximum clock jump to inject in nanoseconds.
	// Defaults to 10 seconds (10,000,000,000 ns).
	ClockJumpMaxNS int64
}

// DefaultDSTConfig returns the default DST configuration.
func DefaultDSTConfig() DSTConfig {
	return DSTConfig{
		Enabled:              false,
		Seed:                 0,
		InitialRealtime:      0,
		VirtualTimeAdvanceNS: 1000, // 1 microsecond per syscall
		ControlSocket:        "",
		MaxSteps:             0,
		MaxTimeNS:            0,
		FaultNetworkDrop:     0.0,
		FaultDiskWrite:       0.0,
		FaultDiskRead:        0.0,
		FaultSyscall:         0.0,
		FaultClockSkew:       0.0,
		FaultClockJump:       0.0,
		ClockSkewMaxNS:       1_000_000_000,  // 1 second
		ClockJumpMaxNS:       10_000_000_000, // 10 seconds
	}
}

// RegisterDSTFlags registers DST-related flags.
func RegisterDSTFlags(flagSet *flag.FlagSet) {
	flagSet.Bool("dst", false, "enable Deterministic Simulation Testing mode. When enabled, uses virtual time and deterministic scheduling for reproducible execution.")
	flagSet.Uint64("dst-seed", 0, "random seed for DST mode. Same seed produces identical execution sequences.")
	flagSet.Int64("dst-initial-time", 0, "initial wall clock time for DST mode (Unix timestamp in nanoseconds). Defaults to Unix epoch.")
	flagSet.Int64("dst-time-advance", 1000, "virtual time advance per syscall in nanoseconds. Defaults to 1000 (1 microsecond).")
	flagSet.String("dst-control-socket", "", "path to Unix socket for Bloodhound DST control. If empty, no control socket is created.")
	flagSet.Uint64("dst-max-steps", 0, "maximum number of simulation steps (0 = unlimited).")
	flagSet.Int64("dst-max-time", 0, "maximum virtual time in nanoseconds (0 = unlimited).")
	flagSet.Float64("dst-fault-network-drop", 0.0, "probability of dropping network packets (0.0-1.0).")
	flagSet.Float64("dst-fault-disk-write", 0.0, "probability of disk write failures (0.0-1.0).")
	flagSet.Float64("dst-fault-disk-read", 0.0, "probability of disk read failures (0.0-1.0).")
	flagSet.Float64("dst-fault-syscall", 0.0, "probability of syscall failures (0.0-1.0).")
	flagSet.Float64("dst-fault-clock-skew", 0.0, "probability of clock skew injection (0.0-1.0). Simulates clock drift.")
	flagSet.Float64("dst-fault-clock-jump", 0.0, "probability of clock jump injection (0.0-1.0). Simulates NTP adjustments.")
	flagSet.Int64("dst-clock-skew-max", 1000000000, "maximum clock skew in nanoseconds. Defaults to 1 second.")
	flagSet.Int64("dst-clock-jump-max", 10000000000, "maximum clock jump in nanoseconds. Defaults to 10 seconds.")
}

// DSTConfigFromFlags creates a DSTConfig from command-line flags.
func DSTConfigFromFlags(flagSet *flag.FlagSet) DSTConfig {
	return DSTConfig{
		Enabled:              flag.Get(flagSet.Lookup("dst").Value).(bool),
		Seed:                 flag.Get(flagSet.Lookup("dst-seed").Value).(uint64),
		InitialRealtime:      flag.Get(flagSet.Lookup("dst-initial-time").Value).(int64),
		VirtualTimeAdvanceNS: flag.Get(flagSet.Lookup("dst-time-advance").Value).(int64),
		ControlSocket:        flag.Get(flagSet.Lookup("dst-control-socket").Value).(string),
		MaxSteps:             flag.Get(flagSet.Lookup("dst-max-steps").Value).(uint64),
		MaxTimeNS:            flag.Get(flagSet.Lookup("dst-max-time").Value).(int64),
		FaultNetworkDrop:     flag.Get(flagSet.Lookup("dst-fault-network-drop").Value).(float64),
		FaultDiskWrite:       flag.Get(flagSet.Lookup("dst-fault-disk-write").Value).(float64),
		FaultDiskRead:        flag.Get(flagSet.Lookup("dst-fault-disk-read").Value).(float64),
		FaultSyscall:         flag.Get(flagSet.Lookup("dst-fault-syscall").Value).(float64),
		FaultClockSkew:       flag.Get(flagSet.Lookup("dst-fault-clock-skew").Value).(float64),
		FaultClockJump:       flag.Get(flagSet.Lookup("dst-fault-clock-jump").Value).(float64),
		ClockSkewMaxNS:       flag.Get(flagSet.Lookup("dst-clock-skew-max").Value).(int64),
		ClockJumpMaxNS:       flag.Get(flagSet.Lookup("dst-clock-jump-max").Value).(int64),
	}
}
