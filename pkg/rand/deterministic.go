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

package rand

import (
	"encoding/binary"
	"io"

	"gvisor.dev/gvisor/pkg/sync"
)

// DeterministicReader implements io.Reader using a ChaCha20-based PRNG.
// It provides deterministic random output given the same seed, enabling
// reproducible execution for Deterministic Simulation Testing (DST).
//
// The implementation uses a simplified ChaCha20 quarter-round construction
// that is suitable for simulation testing but should NOT be used for
// cryptographic purposes in production.
//
// +stateify savable
type DeterministicReader struct {
	mu sync.Mutex

	// state is the ChaCha20 state (16 x 32-bit words).
	state [16]uint32

	// counter tracks how many blocks have been generated.
	counter uint64

	// buffer holds unused random bytes from the last block.
	buffer []byte
}

// NewDeterministicReader creates a new DeterministicReader with the given seed.
// The seed is expanded into the initial ChaCha20 state.
func NewDeterministicReader(seed uint64) *DeterministicReader {
	dr := &DeterministicReader{}
	dr.Seed(seed)
	return dr
}

// Seed resets the RNG state with a new seed.
func (dr *DeterministicReader) Seed(seed uint64) {
	dr.mu.Lock()
	defer dr.mu.Unlock()

	// ChaCha20 constants: "expand 32-byte k"
	dr.state[0] = 0x61707865
	dr.state[1] = 0x3320646e
	dr.state[2] = 0x79622d32
	dr.state[3] = 0x6b206574

	// Expand seed into key (words 4-11)
	// Use seed to generate 8 words deterministically
	for i := 0; i < 8; i++ {
		// Simple expansion: mix seed with index
		dr.state[4+i] = uint32(seed>>uint(i*4)) ^ uint32(i*0x9e3779b9)
	}

	// Counter (words 12-13)
	dr.state[12] = 0
	dr.state[13] = 0

	// Nonce (words 14-15) - derived from seed
	dr.state[14] = uint32(seed)
	dr.state[15] = uint32(seed >> 32)

	dr.counter = 0
	dr.buffer = nil
}

// Read implements io.Reader.
func (dr *DeterministicReader) Read(p []byte) (int, error) {
	dr.mu.Lock()
	defer dr.mu.Unlock()

	n := 0
	for n < len(p) {
		// Use buffered bytes first
		if len(dr.buffer) > 0 {
			copied := copy(p[n:], dr.buffer)
			n += copied
			dr.buffer = dr.buffer[copied:]
			continue
		}

		// Generate a new block
		block := dr.generateBlock()
		dr.buffer = block[:]

		// Increment counter
		dr.counter++
		dr.state[12] = uint32(dr.counter)
		dr.state[13] = uint32(dr.counter >> 32)
	}

	return n, nil
}

// generateBlock generates a 64-byte block of random data using ChaCha20.
func (dr *DeterministicReader) generateBlock() [64]byte {
	// Working state
	var x [16]uint32
	copy(x[:], dr.state[:])

	// 20 rounds (10 double-rounds)
	for i := 0; i < 10; i++ {
		// Column rounds
		quarterRound(&x[0], &x[4], &x[8], &x[12])
		quarterRound(&x[1], &x[5], &x[9], &x[13])
		quarterRound(&x[2], &x[6], &x[10], &x[14])
		quarterRound(&x[3], &x[7], &x[11], &x[15])

		// Diagonal rounds
		quarterRound(&x[0], &x[5], &x[10], &x[15])
		quarterRound(&x[1], &x[6], &x[11], &x[12])
		quarterRound(&x[2], &x[7], &x[8], &x[13])
		quarterRound(&x[3], &x[4], &x[9], &x[14])
	}

	// Add original state
	for i := 0; i < 16; i++ {
		x[i] += dr.state[i]
	}

	// Serialize to bytes
	var block [64]byte
	for i := 0; i < 16; i++ {
		binary.LittleEndian.PutUint32(block[i*4:], x[i])
	}

	return block
}

// quarterRound performs the ChaCha20 quarter round operation.
func quarterRound(a, b, c, d *uint32) {
	*a += *b
	*d ^= *a
	*d = rotl(*d, 16)

	*c += *d
	*b ^= *c
	*b = rotl(*b, 12)

	*a += *b
	*d ^= *a
	*d = rotl(*d, 8)

	*c += *d
	*b ^= *c
	*b = rotl(*b, 7)
}

// rotl performs a left rotation.
func rotl(x uint32, n uint) uint32 {
	return (x << n) | (x >> (32 - n))
}

// GetState returns the current RNG state for checkpointing.
// Returns the state array, counter, and any buffered bytes.
func (dr *DeterministicReader) GetState() (state [16]uint32, counter uint64, buffer []byte) {
	dr.mu.Lock()
	defer dr.mu.Unlock()

	copy(state[:], dr.state[:])
	counter = dr.counter
	if len(dr.buffer) > 0 {
		buffer = make([]byte, len(dr.buffer))
		copy(buffer, dr.buffer)
	}
	return
}

// SetState restores the RNG state from a checkpoint.
func (dr *DeterministicReader) SetState(state [16]uint32, counter uint64, buffer []byte) {
	dr.mu.Lock()
	defer dr.mu.Unlock()

	copy(dr.state[:], state[:])
	dr.counter = counter
	if len(buffer) > 0 {
		dr.buffer = make([]byte, len(buffer))
		copy(dr.buffer, buffer)
	} else {
		dr.buffer = nil
	}
}

// Verify interface compliance
var _ io.Reader = (*DeterministicReader)(nil)
