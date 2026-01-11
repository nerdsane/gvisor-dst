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
}

// DefaultDSTConfig returns the default DST configuration.
func DefaultDSTConfig() DSTConfig {
	return DSTConfig{
		Enabled:              false,
		Seed:                 0,
		InitialRealtime:      0,
		VirtualTimeAdvanceNS: 1000, // 1 microsecond per syscall
	}
}

// RegisterDSTFlags registers DST-related flags.
func RegisterDSTFlags(flagSet *flag.FlagSet) {
	flagSet.Bool("dst", false, "enable Deterministic Simulation Testing mode. When enabled, uses virtual time and deterministic scheduling for reproducible execution.")
	flagSet.Uint64("dst-seed", 0, "random seed for DST mode. Same seed produces identical execution sequences.")
	flagSet.Int64("dst-initial-time", 0, "initial wall clock time for DST mode (Unix timestamp in nanoseconds). Defaults to Unix epoch.")
	flagSet.Int64("dst-time-advance", 1000, "virtual time advance per syscall in nanoseconds. Defaults to 1000 (1 microsecond).")
}

// DSTConfigFromFlags creates a DSTConfig from command-line flags.
func DSTConfigFromFlags(flagSet *flag.FlagSet) DSTConfig {
	return DSTConfig{
		Enabled:              flagSet.Lookup("dst").Value.(flag.Getter).Get().(bool),
		Seed:                 flagSet.Lookup("dst-seed").Value.(flag.Getter).Get().(uint64),
		InitialRealtime:      flagSet.Lookup("dst-initial-time").Value.(flag.Getter).Get().(int64),
		VirtualTimeAdvanceNS: flagSet.Lookup("dst-time-advance").Value.(flag.Getter).Get().(int64),
	}
}
