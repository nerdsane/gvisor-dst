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

// Package dst provides deterministic network endpoints for simulation testing.
// It wraps underlying link endpoints to provide deterministic packet ordering,
// simulated delays, and fault injection capabilities.
package dst

import (
	"sort"

	"gvisor.dev/gvisor/pkg/sync"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

// PacketInfo holds information about a packet in transit.
type PacketInfo struct {
	// ID is a unique identifier for this packet (for ordering).
	ID uint64
	// Protocol is the network protocol number.
	Protocol tcpip.NetworkProtocolNumber
	// Pkt is the packet buffer.
	Pkt *stack.PacketBuffer
	// DeliveryTime is the virtual time when this packet should be delivered.
	// 0 means deliver immediately.
	DeliveryTime uint64
	// SourceEndpoint is the ID of the source endpoint.
	SourceEndpoint EndpointID
	// DestEndpoint is the ID of the destination endpoint.
	DestEndpoint EndpointID
}

// EndpointID uniquely identifies an endpoint in the deterministic network.
type EndpointID uint32

// Endpoint is a deterministic link endpoint that wraps an underlying endpoint.
// It provides deterministic packet ordering and supports simulated delays
// and fault injection for testing.
//
// +stateify savable
type Endpoint struct {
	// id is this endpoint's unique identifier.
	id EndpointID

	// network is the parent deterministic network.
	network *Network

	// mtu is the maximum transmission unit.
	mtu uint32

	// linkAddr is the link address (MAC).
	linkAddr tcpip.LinkAddress

	mu sync.RWMutex
	// +checklocks:mu
	dispatcher stack.NetworkDispatcher
	// +checklocks:mu
	closed bool
}

var _ stack.LinkEndpoint = (*Endpoint)(nil)

// NewEndpoint creates a new deterministic endpoint.
func NewEndpoint(id EndpointID, network *Network, mtu uint32, linkAddr tcpip.LinkAddress) *Endpoint {
	return &Endpoint{
		id:       id,
		network:  network,
		mtu:      mtu,
		linkAddr: linkAddr,
	}
}

// ID returns the endpoint's unique identifier.
func (e *Endpoint) ID() EndpointID {
	return e.id
}

// Close closes the endpoint.
func (e *Endpoint) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closed = true
}

// MTU implements stack.LinkEndpoint.MTU.
func (e *Endpoint) MTU() uint32 {
	return e.mtu
}

// SetMTU implements stack.LinkEndpoint.SetMTU.
func (e *Endpoint) SetMTU(mtu uint32) {
	e.mtu = mtu
}

// Capabilities implements stack.LinkEndpoint.Capabilities.
func (e *Endpoint) Capabilities() stack.LinkEndpointCapabilities {
	return 0
}

// MaxHeaderLength implements stack.LinkEndpoint.MaxHeaderLength.
func (e *Endpoint) MaxHeaderLength() uint16 {
	return 0
}

// LinkAddress implements stack.LinkEndpoint.LinkAddress.
func (e *Endpoint) LinkAddress() tcpip.LinkAddress {
	return e.linkAddr
}

// SetLinkAddress implements stack.LinkEndpoint.SetLinkAddress.
func (e *Endpoint) SetLinkAddress(addr tcpip.LinkAddress) {
	e.linkAddr = addr
}

// Attach implements stack.LinkEndpoint.Attach.
func (e *Endpoint) Attach(dispatcher stack.NetworkDispatcher) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.dispatcher = dispatcher
}

// IsAttached implements stack.LinkEndpoint.IsAttached.
func (e *Endpoint) IsAttached() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.dispatcher != nil
}

// WritePackets implements stack.LinkEndpoint.WritePackets.
// Packets are sent to the deterministic network for controlled delivery.
func (e *Endpoint) WritePackets(pkts stack.PacketBufferList) (int, tcpip.Error) {
	e.mu.RLock()
	if e.closed {
		e.mu.RUnlock()
		return 0, &tcpip.ErrClosedForSend{}
	}
	e.mu.RUnlock()

	n := 0
	for _, pkt := range pkts.AsSlice() {
		if err := e.network.SendPacket(e.id, pkt); err != nil {
			if n == 0 {
				return 0, err
			}
			break
		}
		n++
	}
	return n, nil
}

// Wait implements stack.LinkEndpoint.Wait.
func (e *Endpoint) Wait() {}

// ARPHardwareType implements stack.LinkEndpoint.ARPHardwareType.
func (e *Endpoint) ARPHardwareType() header.ARPHardwareType {
	return header.ARPHardwareEther
}

// AddHeader implements stack.LinkEndpoint.AddHeader.
func (e *Endpoint) AddHeader(pkt *stack.PacketBuffer) {}

// ParseHeader implements stack.LinkEndpoint.ParseHeader.
func (e *Endpoint) ParseHeader(pkt *stack.PacketBuffer) bool {
	return true
}

// SetOnCloseAction implements stack.LinkEndpoint.SetOnCloseAction.
func (e *Endpoint) SetOnCloseAction(func()) {}

// DeliverPacket delivers a packet to this endpoint's dispatcher.
func (e *Endpoint) DeliverPacket(protocol tcpip.NetworkProtocolNumber, pkt *stack.PacketBuffer) {
	e.mu.RLock()
	d := e.dispatcher
	e.mu.RUnlock()

	if d != nil {
		d.DeliverNetworkPacket(protocol, pkt)
	}
}

// NetworkConfig holds configuration for the deterministic network.
type NetworkConfig struct {
	// DefaultDelayNS is the default delay in nanoseconds for packet delivery.
	// 0 means deliver immediately.
	DefaultDelayNS uint64

	// PacketLossProbability is the probability of dropping a packet (0.0 to 1.0).
	// Used for fault injection.
	PacketLossProbability float64

	// PacketReorderProbability is the probability of reordering packets (0.0 to 1.0).
	// Used for fault injection.
	PacketReorderProbability float64
}

// Network manages a set of deterministic endpoints and controls packet delivery
// between them. It ensures deterministic ordering of packet delivery.
type Network struct {
	mu sync.Mutex

	// config holds network configuration.
	config NetworkConfig

	// endpoints maps endpoint IDs to endpoints.
	// +checklocks:mu
	endpoints map[EndpointID]*Endpoint

	// connections defines which endpoints can communicate.
	// connections[src][dst] = true means src can send to dst.
	// +checklocks:mu
	connections map[EndpointID]map[EndpointID]bool

	// pendingPackets holds packets waiting to be delivered.
	// +checklocks:mu
	pendingPackets []*PacketInfo

	// nextPacketID is the next packet ID to assign.
	// +checklocks:mu
	nextPacketID uint64

	// virtualTime is the current virtual time in nanoseconds.
	// +checklocks:mu
	virtualTime uint64

	// listeners receive notifications about network events.
	// +checklocks:mu
	listeners []NetworkListener

	// rng is a deterministic RNG for packet loss/reordering decisions.
	// +checklocks:mu
	rng *deterministicRNG
}

// NetworkListener is notified of network events.
type NetworkListener interface {
	OnPacketSent(info *PacketInfo)
	OnPacketDelivered(info *PacketInfo)
	OnPacketDropped(info *PacketInfo, reason string)
}

// NewNetwork creates a new deterministic network.
func NewNetwork(config NetworkConfig, seed uint64) *Network {
	return &Network{
		config:      config,
		endpoints:   make(map[EndpointID]*Endpoint),
		connections: make(map[EndpointID]map[EndpointID]bool),
		rng:         newDeterministicRNG(seed),
	}
}

// CreateEndpoint creates a new endpoint on this network.
func (n *Network) CreateEndpoint(id EndpointID, mtu uint32, linkAddr tcpip.LinkAddress) *Endpoint {
	n.mu.Lock()
	defer n.mu.Unlock()

	ep := NewEndpoint(id, n, mtu, linkAddr)
	n.endpoints[id] = ep
	return ep
}

// GetEndpoint returns an endpoint by ID, or nil if not found.
func (n *Network) GetEndpoint(id EndpointID) *Endpoint {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.endpoints[id]
}

// Connect allows communication between two endpoints.
func (n *Network) Connect(ep1, ep2 EndpointID) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.connections[ep1] == nil {
		n.connections[ep1] = make(map[EndpointID]bool)
	}
	if n.connections[ep2] == nil {
		n.connections[ep2] = make(map[EndpointID]bool)
	}

	n.connections[ep1][ep2] = true
	n.connections[ep2][ep1] = true
}

// Disconnect prevents communication between two endpoints.
func (n *Network) Disconnect(ep1, ep2 EndpointID) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.connections[ep1] != nil {
		delete(n.connections[ep1], ep2)
	}
	if n.connections[ep2] != nil {
		delete(n.connections[ep2], ep1)
	}
}

// SendPacket sends a packet from the source endpoint.
// The packet is queued for deterministic delivery.
func (n *Network) SendPacket(src EndpointID, pkt *stack.PacketBuffer) tcpip.Error {
	n.mu.Lock()
	defer n.mu.Unlock()

	// Check if source endpoint exists.
	if _, ok := n.endpoints[src]; !ok {
		return &tcpip.ErrInvalidEndpointState{}
	}

	// Find connected destinations.
	dests := n.connections[src]
	if len(dests) == 0 {
		// No connections, packet is dropped.
		return nil
	}

	// Apply packet loss fault injection.
	if n.config.PacketLossProbability > 0 && n.rng.Float64() < n.config.PacketLossProbability {
		// Packet dropped.
		for _, l := range n.listeners {
			l.OnPacketDropped(&PacketInfo{
				ID:             n.nextPacketID,
				SourceEndpoint: src,
			}, "fault injection: packet loss")
		}
		n.nextPacketID++
		return nil
	}

	// Queue packet for each connected destination.
	for dst := range dests {
		info := &PacketInfo{
			ID:             n.nextPacketID,
			Protocol:       pkt.NetworkProtocolNumber,
			Pkt:            pkt.Clone(),
			DeliveryTime:   n.virtualTime + n.config.DefaultDelayNS,
			SourceEndpoint: src,
			DestEndpoint:   dst,
		}
		n.nextPacketID++

		n.pendingPackets = append(n.pendingPackets, info)

		// Notify listeners.
		for _, l := range n.listeners {
			l.OnPacketSent(info)
		}
	}

	// Apply packet reordering fault injection.
	if n.config.PacketReorderProbability > 0 && n.rng.Float64() < n.config.PacketReorderProbability {
		n.shufflePendingPackets()
	}

	return nil
}

// DeliverPendingPackets delivers all packets scheduled up to the given virtual time.
// Returns the number of packets delivered.
func (n *Network) DeliverPendingPackets(upToTime uint64) int {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.virtualTime = upToTime

	// Sort packets by (delivery time, packet ID) for deterministic ordering.
	sort.Slice(n.pendingPackets, func(i, j int) bool {
		if n.pendingPackets[i].DeliveryTime != n.pendingPackets[j].DeliveryTime {
			return n.pendingPackets[i].DeliveryTime < n.pendingPackets[j].DeliveryTime
		}
		return n.pendingPackets[i].ID < n.pendingPackets[j].ID
	})

	// Find packets ready for delivery.
	delivered := 0
	remaining := make([]*PacketInfo, 0, len(n.pendingPackets))

	for _, info := range n.pendingPackets {
		if info.DeliveryTime <= upToTime {
			// Deliver this packet.
			ep := n.endpoints[info.DestEndpoint]
			if ep != nil {
				// Release lock during delivery to avoid deadlock.
				n.mu.Unlock()
				ep.DeliverPacket(info.Protocol, info.Pkt)
				n.mu.Lock()

				// Notify listeners.
				for _, l := range n.listeners {
					l.OnPacketDelivered(info)
				}
			}
			info.Pkt.DecRef()
			delivered++
		} else {
			remaining = append(remaining, info)
		}
	}

	n.pendingPackets = remaining
	return delivered
}

// DeliverAllPendingPackets delivers all pending packets regardless of virtual time.
func (n *Network) DeliverAllPendingPackets() int {
	return n.DeliverPendingPackets(^uint64(0))
}

// GetVirtualTime returns the current virtual time.
func (n *Network) GetVirtualTime() uint64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.virtualTime
}

// SetVirtualTime sets the current virtual time.
func (n *Network) SetVirtualTime(t uint64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.virtualTime = t
}

// AddListener adds a network event listener.
func (n *Network) AddListener(l NetworkListener) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.listeners = append(n.listeners, l)
}

// GetPendingPacketCount returns the number of packets waiting to be delivered.
func (n *Network) GetPendingPacketCount() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.pendingPackets)
}

// shufflePendingPackets deterministically shuffles pending packets.
func (n *Network) shufflePendingPackets() {
	// Fisher-Yates shuffle using deterministic RNG.
	for i := len(n.pendingPackets) - 1; i > 0; i-- {
		j := n.rng.Intn(i + 1)
		n.pendingPackets[i], n.pendingPackets[j] = n.pendingPackets[j], n.pendingPackets[i]
	}
}

// NetworkState represents the network state for checkpointing.
type NetworkState struct {
	VirtualTime    uint64
	NextPacketID   uint64
	RNGState       uint64
	PendingPackets []PacketStateInfo
}

// PacketStateInfo is a serializable representation of PacketInfo.
type PacketStateInfo struct {
	ID             uint64
	Protocol       tcpip.NetworkProtocolNumber
	DeliveryTime   uint64
	SourceEndpoint EndpointID
	DestEndpoint   EndpointID
	// PacketData would contain serialized packet data in a full implementation.
}

// GetState returns the network state for checkpointing.
func (n *Network) GetState() NetworkState {
	n.mu.Lock()
	defer n.mu.Unlock()

	pendingInfo := make([]PacketStateInfo, 0, len(n.pendingPackets))
	for _, p := range n.pendingPackets {
		pendingInfo = append(pendingInfo, PacketStateInfo{
			ID:             p.ID,
			Protocol:       p.Protocol,
			DeliveryTime:   p.DeliveryTime,
			SourceEndpoint: p.SourceEndpoint,
			DestEndpoint:   p.DestEndpoint,
		})
	}

	return NetworkState{
		VirtualTime:    n.virtualTime,
		NextPacketID:   n.nextPacketID,
		RNGState:       n.rng.state,
		PendingPackets: pendingInfo,
	}
}

// SetState restores the network state from a checkpoint.
// Note: This only restores metadata; packet data must be handled separately.
func (n *Network) SetState(state NetworkState) {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.virtualTime = state.VirtualTime
	n.nextPacketID = state.NextPacketID
	n.rng.state = state.RNGState

	// Clear pending packets - in a full implementation, we'd restore packet data.
	n.pendingPackets = nil
}

// deterministicRNG is a simple deterministic random number generator.
type deterministicRNG struct {
	state uint64
}

func newDeterministicRNG(seed uint64) *deterministicRNG {
	return &deterministicRNG{state: seed}
}

// Uint64 returns a deterministic pseudo-random uint64.
func (r *deterministicRNG) Uint64() uint64 {
	// Simple xorshift64 algorithm.
	r.state ^= r.state << 13
	r.state ^= r.state >> 7
	r.state ^= r.state << 17
	return r.state
}

// Float64 returns a deterministic pseudo-random float64 in [0, 1).
func (r *deterministicRNG) Float64() float64 {
	return float64(r.Uint64()) / float64(^uint64(0))
}

// Intn returns a deterministic pseudo-random int in [0, n).
func (r *deterministicRNG) Intn(n int) int {
	return int(r.Uint64() % uint64(n))
}
