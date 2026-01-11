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

package time

// DSTClocksConfig contains configuration for creating clocks in DST mode.
type DSTClocksConfig struct {
	// Enabled indicates whether DST mode is active.
	Enabled bool

	// InitialRealtime is the initial wall clock time in nanoseconds since Unix epoch.
	InitialRealtime int64

	// InitialMonotonic is the initial monotonic time in nanoseconds.
	InitialMonotonic int64
}

// NewClocks creates a Clocks implementation based on the DST configuration.
// If DST is enabled, returns VirtualClocks for deterministic execution.
// Otherwise, returns CalibratedClocks for normal host-synchronized time.
func NewClocks(cfg DSTClocksConfig) Clocks {
	if cfg.Enabled {
		return NewVirtualClocks(VirtualClocksConfig{
			InitialRealtime:  cfg.InitialRealtime,
			InitialMonotonic: cfg.InitialMonotonic,
			Frequency:        1_000_000_000, // 1 GHz
		})
	}
	return NewCalibratedClocks()
}

// GetVirtualClocks attempts to get the VirtualClocks from a Clocks interface.
// Returns nil if the Clocks is not a VirtualClocks (i.e., DST mode is not enabled).
func GetVirtualClocks(c Clocks) *VirtualClocks {
	vc, ok := c.(*VirtualClocks)
	if !ok {
		return nil
	}
	return vc
}

// IsVirtualClocks returns true if the Clocks is a VirtualClocks instance.
func IsVirtualClocks(c Clocks) bool {
	_, ok := c.(*VirtualClocks)
	return ok
}
