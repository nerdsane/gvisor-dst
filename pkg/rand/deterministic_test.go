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
	"bytes"
	"testing"
)

func TestDeterministicReaderBasic(t *testing.T) {
	dr := NewDeterministicReader(12345)

	buf := make([]byte, 32)
	n, err := dr.Read(buf)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if n != 32 {
		t.Errorf("Read returned %d bytes, want 32", n)
	}

	// Verify bytes are not all zero
	allZero := true
	for _, b := range buf {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Error("Read returned all zeros")
	}
}

func TestDeterministicReaderDeterminism(t *testing.T) {
	// Create two readers with the same seed
	dr1 := NewDeterministicReader(12345)
	dr2 := NewDeterministicReader(12345)

	buf1 := make([]byte, 1024)
	buf2 := make([]byte, 1024)

	dr1.Read(buf1)
	dr2.Read(buf2)

	if !bytes.Equal(buf1, buf2) {
		t.Error("Same seed produced different output")
	}
}

func TestDeterministicReaderDifferentSeeds(t *testing.T) {
	dr1 := NewDeterministicReader(12345)
	dr2 := NewDeterministicReader(54321)

	buf1 := make([]byte, 64)
	buf2 := make([]byte, 64)

	dr1.Read(buf1)
	dr2.Read(buf2)

	if bytes.Equal(buf1, buf2) {
		t.Error("Different seeds produced identical output")
	}
}

func TestDeterministicReaderSequentialReads(t *testing.T) {
	dr := NewDeterministicReader(12345)

	// Read in chunks
	chunk1 := make([]byte, 32)
	chunk2 := make([]byte, 32)
	dr.Read(chunk1)
	dr.Read(chunk2)

	// Read all at once with new reader
	dr2 := NewDeterministicReader(12345)
	combined := make([]byte, 64)
	dr2.Read(combined)

	// Should be identical
	if !bytes.Equal(chunk1, combined[:32]) {
		t.Error("First chunk doesn't match")
	}
	if !bytes.Equal(chunk2, combined[32:]) {
		t.Error("Second chunk doesn't match")
	}
}

func TestDeterministicReaderReseed(t *testing.T) {
	dr := NewDeterministicReader(12345)

	buf1 := make([]byte, 32)
	dr.Read(buf1)

	// Reseed
	dr.Seed(12345)

	buf2 := make([]byte, 32)
	dr.Read(buf2)

	// Should produce same output after reseed
	if !bytes.Equal(buf1, buf2) {
		t.Error("Reseed didn't reset to same state")
	}
}

func TestDeterministicReaderCheckpoint(t *testing.T) {
	dr := NewDeterministicReader(12345)

	// Read some bytes
	pre := make([]byte, 100)
	dr.Read(pre)

	// Checkpoint
	state, counter, buffer := dr.GetState()

	// Read more bytes
	post := make([]byte, 100)
	dr.Read(post)

	// Restore checkpoint
	dr.SetState(state, counter, buffer)

	// Read should produce same bytes as post
	restored := make([]byte, 100)
	dr.Read(restored)

	if !bytes.Equal(post, restored) {
		t.Error("Checkpoint restore didn't work")
	}
}

func TestDeterministicReaderLargeRead(t *testing.T) {
	dr := NewDeterministicReader(12345)

	// Read more than one block (64 bytes)
	buf := make([]byte, 1000)
	n, err := dr.Read(buf)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if n != 1000 {
		t.Errorf("Read returned %d bytes, want 1000", n)
	}
}

func TestDeterministicReaderSmallReads(t *testing.T) {
	dr := NewDeterministicReader(12345)

	// Many small reads
	var result []byte
	for i := 0; i < 100; i++ {
		buf := make([]byte, 1)
		dr.Read(buf)
		result = append(result, buf...)
	}

	// Compare with single read
	dr2 := NewDeterministicReader(12345)
	expected := make([]byte, 100)
	dr2.Read(expected)

	if !bytes.Equal(result, expected) {
		t.Error("Small reads don't match single large read")
	}
}

func TestDSTEnableDisable(t *testing.T) {
	// Ensure DST is disabled initially
	DisableDST()

	if IsDSTEnabled() {
		t.Error("DST should be disabled initially")
	}

	// Enable DST
	EnableDST(12345)

	if !IsDSTEnabled() {
		t.Error("DST should be enabled after EnableDST")
	}

	// Get deterministic reader
	dr := GetDeterministicReader()
	if dr == nil {
		t.Error("GetDeterministicReader returned nil")
	}

	// Read some bytes
	buf := make([]byte, 32)
	Read(buf)

	// Disable DST
	DisableDST()

	if IsDSTEnabled() {
		t.Error("DST should be disabled after DisableDST")
	}

	if GetDeterministicReader() != nil {
		t.Error("GetDeterministicReader should return nil after disable")
	}
}

func TestDSTDeterminism(t *testing.T) {
	run := func(seed uint64) []byte {
		DisableDST()
		EnableDST(seed)
		buf := make([]byte, 100)
		Read(buf)
		DisableDST()
		return buf
	}

	result1 := run(12345)
	result2 := run(12345)

	if !bytes.Equal(result1, result2) {
		t.Error("DST mode is not deterministic")
	}
}

func TestDSTStateCheckpoint(t *testing.T) {
	DisableDST()
	EnableDST(12345)

	// Read some bytes
	pre := make([]byte, 50)
	Read(pre)

	// Get state
	state := GetDSTState()
	if state == nil {
		t.Fatal("GetDSTState returned nil")
	}

	// Read more bytes
	post := make([]byte, 50)
	Read(post)

	// Restore state
	RestoreDSTState(state)

	// Read should produce same bytes as post
	restored := make([]byte, 50)
	Read(restored)

	if !bytes.Equal(post, restored) {
		t.Error("DST state restore didn't work")
	}

	DisableDST()
}

func TestDSTReseed(t *testing.T) {
	DisableDST()
	EnableDST(12345)

	buf1 := make([]byte, 32)
	Read(buf1)

	// Reseed
	Reseed(12345)

	buf2 := make([]byte, 32)
	Read(buf2)

	if !bytes.Equal(buf1, buf2) {
		t.Error("Reseed didn't reset RNG state")
	}

	DisableDST()
}

func TestDSTStateNil(t *testing.T) {
	DisableDST()

	// GetDSTState should return nil when disabled
	state := GetDSTState()
	if state != nil {
		t.Error("GetDSTState should return nil when DST is disabled")
	}

	// RestoreDSTState with nil should not panic
	RestoreDSTState(nil)

	if IsDSTEnabled() {
		t.Error("RestoreDSTState(nil) should not enable DST")
	}
}

// BenchmarkDeterministicReader measures performance of deterministic RNG.
func BenchmarkDeterministicReader(b *testing.B) {
	dr := NewDeterministicReader(12345)
	buf := make([]byte, 4096)

	b.SetBytes(4096)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		dr.Read(buf)
	}
}

// BenchmarkDeterministicReaderSmall measures small read performance.
func BenchmarkDeterministicReaderSmall(b *testing.B) {
	dr := NewDeterministicReader(12345)
	buf := make([]byte, 8)

	b.SetBytes(8)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		dr.Read(buf)
	}
}
