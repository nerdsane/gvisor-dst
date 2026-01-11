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
	"gvisor.dev/gvisor/pkg/sync"
)

// DSTNetworkConfig holds DST-specific network configuration.
type DSTNetworkConfig struct {
	// Enabled indicates whether DST networking is active.
	Enabled bool

	// Seed is the random seed for deterministic behavior.
	Seed uint64

	// DefaultDelayNS is the default network delay in nanoseconds.
	DefaultDelayNS uint64

	// PacketLossProbability for fault injection (0.0 to 1.0).
	PacketLossProbability float64

	// PacketReorderProbability for fault injection (0.0 to 1.0).
	PacketReorderProbability float64
}

// dstGlobalState holds global DST network state.
var dstGlobalState struct {
	mu      sync.RWMutex
	config  DSTNetworkConfig
	network *Network
}

// EnableDSTNetwork enables deterministic networking.
func EnableDSTNetwork(config DSTNetworkConfig) {
	dstGlobalState.mu.Lock()
	defer dstGlobalState.mu.Unlock()

	dstGlobalState.config = config

	if config.Enabled {
		dstGlobalState.network = NewNetwork(NetworkConfig{
			DefaultDelayNS:           config.DefaultDelayNS,
			PacketLossProbability:    config.PacketLossProbability,
			PacketReorderProbability: config.PacketReorderProbability,
		}, config.Seed)
	}
}

// DisableDSTNetwork disables deterministic networking.
func DisableDSTNetwork() {
	dstGlobalState.mu.Lock()
	defer dstGlobalState.mu.Unlock()

	dstGlobalState.config.Enabled = false
	dstGlobalState.network = nil
}

// IsDSTNetworkEnabled returns true if DST networking is enabled.
func IsDSTNetworkEnabled() bool {
	dstGlobalState.mu.RLock()
	defer dstGlobalState.mu.RUnlock()
	return dstGlobalState.config.Enabled && dstGlobalState.network != nil
}

// GetDSTNetwork returns the global DST network, or nil if not enabled.
func GetDSTNetwork() *Network {
	dstGlobalState.mu.RLock()
	defer dstGlobalState.mu.RUnlock()
	return dstGlobalState.network
}

// GetDSTNetworkConfig returns the current DST network configuration.
func GetDSTNetworkConfig() DSTNetworkConfig {
	dstGlobalState.mu.RLock()
	defer dstGlobalState.mu.RUnlock()
	return dstGlobalState.config
}

// DSTNetworkState represents the complete DST network state for checkpointing.
type DSTNetworkState struct {
	Config       DSTNetworkConfig
	NetworkState NetworkState
}

// GetDSTNetworkState returns the current DST network state for checkpointing.
func GetDSTNetworkState() *DSTNetworkState {
	dstGlobalState.mu.RLock()
	defer dstGlobalState.mu.RUnlock()

	if !dstGlobalState.config.Enabled || dstGlobalState.network == nil {
		return nil
	}

	return &DSTNetworkState{
		Config:       dstGlobalState.config,
		NetworkState: dstGlobalState.network.GetState(),
	}
}

// RestoreDSTNetworkState restores DST network state from a checkpoint.
func RestoreDSTNetworkState(state *DSTNetworkState) {
	if state == nil {
		DisableDSTNetwork()
		return
	}

	dstGlobalState.mu.Lock()
	defer dstGlobalState.mu.Unlock()

	dstGlobalState.config = state.Config

	if state.Config.Enabled {
		if dstGlobalState.network == nil {
			dstGlobalState.network = NewNetwork(NetworkConfig{
				DefaultDelayNS:           state.Config.DefaultDelayNS,
				PacketLossProbability:    state.Config.PacketLossProbability,
				PacketReorderProbability: state.Config.PacketReorderProbability,
			}, state.Config.Seed)
		}
		dstGlobalState.network.SetState(state.NetworkState)
	}
}

// SetPacketLossProbability dynamically updates the packet loss probability.
// This is useful for fault injection during simulation.
func SetPacketLossProbability(prob float64) {
	dstGlobalState.mu.Lock()
	defer dstGlobalState.mu.Unlock()

	if dstGlobalState.network != nil {
		dstGlobalState.network.config.PacketLossProbability = prob
		dstGlobalState.config.PacketLossProbability = prob
	}
}

// SetPacketReorderProbability dynamically updates the packet reorder probability.
// This is useful for fault injection during simulation.
func SetPacketReorderProbability(prob float64) {
	dstGlobalState.mu.Lock()
	defer dstGlobalState.mu.Unlock()

	if dstGlobalState.network != nil {
		dstGlobalState.network.config.PacketReorderProbability = prob
		dstGlobalState.config.PacketReorderProbability = prob
	}
}

// SetNetworkDelay dynamically updates the default network delay.
func SetNetworkDelay(delayNS uint64) {
	dstGlobalState.mu.Lock()
	defer dstGlobalState.mu.Unlock()

	if dstGlobalState.network != nil {
		dstGlobalState.network.config.DefaultDelayNS = delayNS
		dstGlobalState.config.DefaultDelayNS = delayNS
	}
}

// AdvanceNetworkTime advances the network virtual time and delivers pending packets.
// Returns the number of packets delivered.
func AdvanceNetworkTime(deltaTimeNS uint64) int {
	dstGlobalState.mu.RLock()
	network := dstGlobalState.network
	dstGlobalState.mu.RUnlock()

	if network == nil {
		return 0
	}

	currentTime := network.GetVirtualTime()
	return network.DeliverPendingPackets(currentTime + deltaTimeNS)
}

// DeliverAllPackets delivers all pending packets immediately.
// Returns the number of packets delivered.
func DeliverAllPackets() int {
	dstGlobalState.mu.RLock()
	network := dstGlobalState.network
	dstGlobalState.mu.RUnlock()

	if network == nil {
		return 0
	}

	return network.DeliverAllPendingPackets()
}

// CreatePartition creates a network partition between two sets of endpoints.
// Endpoints in set A cannot communicate with endpoints in set B.
func CreatePartition(setA, setB []EndpointID) {
	dstGlobalState.mu.RLock()
	network := dstGlobalState.network
	dstGlobalState.mu.RUnlock()

	if network == nil {
		return
	}

	for _, a := range setA {
		for _, b := range setB {
			network.Disconnect(a, b)
		}
	}
}

// HealPartition heals a network partition, allowing communication again.
func HealPartition(setA, setB []EndpointID) {
	dstGlobalState.mu.RLock()
	network := dstGlobalState.network
	dstGlobalState.mu.RUnlock()

	if network == nil {
		return
	}

	for _, a := range setA {
		for _, b := range setB {
			network.Connect(a, b)
		}
	}
}
