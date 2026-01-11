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
	"sync"
	"testing"

	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

// mockDispatcher records delivered packets for testing.
type mockDispatcher struct {
	mu       sync.Mutex
	packets  []tcpip.NetworkProtocolNumber
	data     [][]byte
	notifyCh chan struct{}
}

func newMockDispatcher() *mockDispatcher {
	return &mockDispatcher{
		notifyCh: make(chan struct{}, 100),
	}
}

func (d *mockDispatcher) DeliverNetworkPacket(protocol tcpip.NetworkProtocolNumber, pkt *stack.PacketBuffer) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.packets = append(d.packets, protocol)
	// Clone the data before the packet is released.
	data := pkt.ToBuffer().Flatten()
	d.data = append(d.data, data)
	select {
	case d.notifyCh <- struct{}{}:
	default:
	}
}

func (d *mockDispatcher) DeliverLinkPacket(protocol tcpip.NetworkProtocolNumber, pkt *stack.PacketBuffer) {
}

func (d *mockDispatcher) getPacketCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.packets)
}

func TestNetworkBasic(t *testing.T) {
	network := NewNetwork(NetworkConfig{}, 12345)

	// Create two endpoints.
	ep1 := network.CreateEndpoint(1, 1500, "aa:bb:cc:dd:ee:01")
	ep2 := network.CreateEndpoint(2, 1500, "aa:bb:cc:dd:ee:02")

	// Connect them.
	network.Connect(1, 2)

	// Attach dispatchers.
	d1 := newMockDispatcher()
	d2 := newMockDispatcher()
	ep1.Attach(d1)
	ep2.Attach(d2)

	// Verify endpoints are attached.
	if !ep1.IsAttached() {
		t.Error("ep1 should be attached")
	}
	if !ep2.IsAttached() {
		t.Error("ep2 should be attached")
	}
}

func TestNetworkPacketDelivery(t *testing.T) {
	network := NewNetwork(NetworkConfig{
		DefaultDelayNS: 0, // Immediate delivery.
	}, 12345)

	ep1 := network.CreateEndpoint(1, 1500, "aa:bb:cc:dd:ee:01")
	ep2 := network.CreateEndpoint(2, 1500, "aa:bb:cc:dd:ee:02")

	network.Connect(1, 2)

	d2 := newMockDispatcher()
	ep2.Attach(d2)

	// Send a packet from ep1.
	pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{})
	pkt.NetworkProtocolNumber = 0x0800 // IPv4
	defer pkt.DecRef()

	var pkts stack.PacketBufferList
	pkts.PushBack(pkt)
	n, err := ep1.WritePackets(pkts)
	if err != nil {
		t.Fatalf("WritePackets failed: %v", err)
	}
	if n != 1 {
		t.Errorf("WritePackets returned %d, want 1", n)
	}

	// Deliver pending packets.
	delivered := network.DeliverAllPendingPackets()
	if delivered != 1 {
		t.Errorf("DeliverAllPendingPackets returned %d, want 1", delivered)
	}

	// Verify packet was delivered to ep2.
	if d2.getPacketCount() != 1 {
		t.Errorf("ep2 received %d packets, want 1", d2.getPacketCount())
	}
}

func TestNetworkDeterminism(t *testing.T) {
	// Run the same sequence twice and verify identical results.
	runSequence := func(seed uint64) []int {
		network := NewNetwork(NetworkConfig{
			DefaultDelayNS: 1000, // 1us delay.
		}, seed)

		ep1 := network.CreateEndpoint(1, 1500, "aa:bb:cc:dd:ee:01")
		ep2 := network.CreateEndpoint(2, 1500, "aa:bb:cc:dd:ee:02")
		ep3 := network.CreateEndpoint(3, 1500, "aa:bb:cc:dd:ee:03")

		network.Connect(1, 2)
		network.Connect(1, 3)
		network.Connect(2, 3)

		d1 := newMockDispatcher()
		d2 := newMockDispatcher()
		d3 := newMockDispatcher()
		ep1.Attach(d1)
		ep2.Attach(d2)
		ep3.Attach(d3)

		// Send packets in a specific order.
		for i := 0; i < 10; i++ {
			pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{})
			pkt.NetworkProtocolNumber = tcpip.NetworkProtocolNumber(i)
			var pkts stack.PacketBufferList
			pkts.PushBack(pkt)

			switch i % 3 {
			case 0:
				ep1.WritePackets(pkts)
			case 1:
				ep2.WritePackets(pkts)
			case 2:
				ep3.WritePackets(pkts)
			}
			pkt.DecRef()
		}

		// Deliver all packets.
		network.DeliverAllPendingPackets()

		// Return delivery counts.
		return []int{d1.getPacketCount(), d2.getPacketCount(), d3.getPacketCount()}
	}

	result1 := runSequence(12345)
	result2 := runSequence(12345)

	for i := range result1 {
		if result1[i] != result2[i] {
			t.Errorf("Results differ at position %d: %d vs %d", i, result1[i], result2[i])
		}
	}
}

func TestNetworkDelayedDelivery(t *testing.T) {
	network := NewNetwork(NetworkConfig{
		DefaultDelayNS: 1000000, // 1ms delay.
	}, 12345)

	ep1 := network.CreateEndpoint(1, 1500, "aa:bb:cc:dd:ee:01")
	ep2 := network.CreateEndpoint(2, 1500, "aa:bb:cc:dd:ee:02")

	network.Connect(1, 2)

	d2 := newMockDispatcher()
	ep2.Attach(d2)

	// Send a packet.
	pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{})
	var pkts stack.PacketBufferList
	pkts.PushBack(pkt)
	ep1.WritePackets(pkts)
	pkt.DecRef()

	// Packet should not be delivered yet (time is 0).
	delivered := network.DeliverPendingPackets(0)
	if delivered != 0 {
		t.Errorf("DeliverPendingPackets(0) returned %d, want 0", delivered)
	}
	if d2.getPacketCount() != 0 {
		t.Errorf("ep2 received %d packets prematurely, want 0", d2.getPacketCount())
	}

	// Advance time and deliver.
	delivered = network.DeliverPendingPackets(2000000) // 2ms
	if delivered != 1 {
		t.Errorf("DeliverPendingPackets(2ms) returned %d, want 1", delivered)
	}
	if d2.getPacketCount() != 1 {
		t.Errorf("ep2 received %d packets, want 1", d2.getPacketCount())
	}
}

func TestNetworkDisconnect(t *testing.T) {
	network := NewNetwork(NetworkConfig{}, 12345)

	ep1 := network.CreateEndpoint(1, 1500, "aa:bb:cc:dd:ee:01")
	ep2 := network.CreateEndpoint(2, 1500, "aa:bb:cc:dd:ee:02")

	network.Connect(1, 2)

	d2 := newMockDispatcher()
	ep2.Attach(d2)

	// Send a packet - should be queued.
	pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{})
	var pkts stack.PacketBufferList
	pkts.PushBack(pkt)
	ep1.WritePackets(pkts)
	pkt.DecRef()

	network.DeliverAllPendingPackets()
	if d2.getPacketCount() != 1 {
		t.Errorf("ep2 received %d packets, want 1", d2.getPacketCount())
	}

	// Disconnect and try again.
	network.Disconnect(1, 2)

	pkt2 := stack.NewPacketBuffer(stack.PacketBufferOptions{})
	var pkts2 stack.PacketBufferList
	pkts2.PushBack(pkt2)
	ep1.WritePackets(pkts2)
	pkt2.DecRef()

	network.DeliverAllPendingPackets()

	// Should still be 1 (disconnected, packet dropped).
	if d2.getPacketCount() != 1 {
		t.Errorf("ep2 received %d packets after disconnect, want 1", d2.getPacketCount())
	}
}

func TestNetworkCheckpointRestore(t *testing.T) {
	network := NewNetwork(NetworkConfig{
		DefaultDelayNS: 1000000, // 1ms.
	}, 12345)

	ep1 := network.CreateEndpoint(1, 1500, "aa:bb:cc:dd:ee:01")
	ep2 := network.CreateEndpoint(2, 1500, "aa:bb:cc:dd:ee:02")

	network.Connect(1, 2)

	d2 := newMockDispatcher()
	ep2.Attach(d2)

	// Send some packets.
	for i := 0; i < 5; i++ {
		pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{})
		var pkts stack.PacketBufferList
		pkts.PushBack(pkt)
		ep1.WritePackets(pkts)
		pkt.DecRef()
	}

	// Take checkpoint.
	state := network.GetState()

	// Deliver some packets.
	network.DeliverPendingPackets(500000) // 0.5ms

	// Restore checkpoint.
	network.SetState(state)

	// Verify state was restored.
	restoredState := network.GetState()
	if restoredState.VirtualTime != state.VirtualTime {
		t.Errorf("VirtualTime not restored: got %d, want %d",
			restoredState.VirtualTime, state.VirtualTime)
	}
	if restoredState.NextPacketID != state.NextPacketID {
		t.Errorf("NextPacketID not restored: got %d, want %d",
			restoredState.NextPacketID, state.NextPacketID)
	}
}

func TestDSTNetworkEnableDisable(t *testing.T) {
	// Ensure disabled initially.
	DisableDSTNetwork()

	if IsDSTNetworkEnabled() {
		t.Error("DST network should be disabled initially")
	}

	// Enable.
	EnableDSTNetwork(DSTNetworkConfig{
		Enabled: true,
		Seed:    12345,
	})

	if !IsDSTNetworkEnabled() {
		t.Error("DST network should be enabled")
	}

	network := GetDSTNetwork()
	if network == nil {
		t.Error("GetDSTNetwork should return non-nil")
	}

	// Disable.
	DisableDSTNetwork()

	if IsDSTNetworkEnabled() {
		t.Error("DST network should be disabled")
	}

	if GetDSTNetwork() != nil {
		t.Error("GetDSTNetwork should return nil")
	}
}

func TestDSTNetworkState(t *testing.T) {
	DisableDSTNetwork()
	EnableDSTNetwork(DSTNetworkConfig{
		Enabled:        true,
		Seed:           12345,
		DefaultDelayNS: 1000,
	})

	network := GetDSTNetwork()
	network.CreateEndpoint(1, 1500, "aa:bb:cc:dd:ee:01")
	network.CreateEndpoint(2, 1500, "aa:bb:cc:dd:ee:02")
	network.Connect(1, 2)

	// Advance virtual time.
	network.SetVirtualTime(5000)

	// Get state.
	state := GetDSTNetworkState()
	if state == nil {
		t.Fatal("GetDSTNetworkState returned nil")
	}

	// Modify state.
	network.SetVirtualTime(10000)

	// Restore.
	RestoreDSTNetworkState(state)

	// Verify.
	network = GetDSTNetwork()
	if network.GetVirtualTime() != 5000 {
		t.Errorf("VirtualTime not restored: got %d, want 5000", network.GetVirtualTime())
	}

	DisableDSTNetwork()
}

func TestNetworkPacketLoss(t *testing.T) {
	// Test with 100% packet loss.
	network := NewNetwork(NetworkConfig{
		PacketLossProbability: 1.0, // 100% loss.
	}, 12345)

	ep1 := network.CreateEndpoint(1, 1500, "aa:bb:cc:dd:ee:01")
	ep2 := network.CreateEndpoint(2, 1500, "aa:bb:cc:dd:ee:02")

	network.Connect(1, 2)

	d2 := newMockDispatcher()
	ep2.Attach(d2)

	// Send packets - all should be dropped.
	for i := 0; i < 10; i++ {
		pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{})
		var pkts stack.PacketBufferList
		pkts.PushBack(pkt)
		ep1.WritePackets(pkts)
		pkt.DecRef()
	}

	network.DeliverAllPendingPackets()

	if d2.getPacketCount() != 0 {
		t.Errorf("ep2 received %d packets with 100%% loss, want 0", d2.getPacketCount())
	}
}

func TestNetworkFaultInjectionHelpers(t *testing.T) {
	DisableDSTNetwork()
	EnableDSTNetwork(DSTNetworkConfig{
		Enabled: true,
		Seed:    12345,
	})

	// Test setting packet loss.
	SetPacketLossProbability(0.5)
	config := GetDSTNetworkConfig()
	if config.PacketLossProbability != 0.5 {
		t.Errorf("PacketLossProbability not set: got %f, want 0.5", config.PacketLossProbability)
	}

	// Test setting packet reorder.
	SetPacketReorderProbability(0.3)
	config = GetDSTNetworkConfig()
	if config.PacketReorderProbability != 0.3 {
		t.Errorf("PacketReorderProbability not set: got %f, want 0.3", config.PacketReorderProbability)
	}

	// Test setting delay.
	SetNetworkDelay(5000)
	config = GetDSTNetworkConfig()
	if config.DefaultDelayNS != 5000 {
		t.Errorf("DefaultDelayNS not set: got %d, want 5000", config.DefaultDelayNS)
	}

	DisableDSTNetwork()
}

// mockNetworkListener records network events.
type mockNetworkListener struct {
	mu        sync.Mutex
	sent      int
	delivered int
	dropped   int
}

func (l *mockNetworkListener) OnPacketSent(info *PacketInfo) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sent++
}

func (l *mockNetworkListener) OnPacketDelivered(info *PacketInfo) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.delivered++
}

func (l *mockNetworkListener) OnPacketDropped(info *PacketInfo, reason string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.dropped++
}

func TestNetworkListener(t *testing.T) {
	network := NewNetwork(NetworkConfig{}, 12345)
	listener := &mockNetworkListener{}
	network.AddListener(listener)

	ep1 := network.CreateEndpoint(1, 1500, "aa:bb:cc:dd:ee:01")
	ep2 := network.CreateEndpoint(2, 1500, "aa:bb:cc:dd:ee:02")

	network.Connect(1, 2)

	d2 := newMockDispatcher()
	ep2.Attach(d2)

	// Send packets.
	for i := 0; i < 5; i++ {
		pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{})
		var pkts stack.PacketBufferList
		pkts.PushBack(pkt)
		ep1.WritePackets(pkts)
		pkt.DecRef()
	}

	if listener.sent != 5 {
		t.Errorf("listener.sent = %d, want 5", listener.sent)
	}

	network.DeliverAllPendingPackets()

	if listener.delivered != 5 {
		t.Errorf("listener.delivered = %d, want 5", listener.delivered)
	}
}

func BenchmarkNetworkSendDelivery(b *testing.B) {
	network := NewNetwork(NetworkConfig{}, 12345)

	ep1 := network.CreateEndpoint(1, 1500, "aa:bb:cc:dd:ee:01")
	ep2 := network.CreateEndpoint(2, 1500, "aa:bb:cc:dd:ee:02")

	network.Connect(1, 2)

	d2 := newMockDispatcher()
	ep2.Attach(d2)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{})
		var pkts stack.PacketBufferList
		pkts.PushBack(pkt)
		ep1.WritePackets(pkts)
		network.DeliverAllPendingPackets()
		pkt.DecRef()
	}
}
