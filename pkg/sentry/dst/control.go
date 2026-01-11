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
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"

	"gvisor.dev/gvisor/pkg/log"
	"gvisor.dev/gvisor/pkg/sync"
)

// ControlCommand represents a command from Bloodhound to gVisor.
type ControlCommand struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// ControlResponse represents a response from gVisor to Bloodhound.
type ControlResponse struct {
	Status string          `json:"status"` // "ok" or "error"
	Data   json.RawMessage `json:"data,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// Command types.
const (
	CmdStep                  = "Step"
	CmdPause                 = "Pause"
	CmdResume                = "Resume"
	CmdGetState              = "GetState"
	CmdSnapshot              = "Snapshot"
	CmdRestore               = "Restore"
	CmdSetFaultProbabilities = "SetFaultProbabilities"
	CmdScheduleFault         = "ScheduleFault"
	CmdCancelFault           = "CancelFault"
	CmdAddProperty           = "AddProperty"
	CmdCheckProperties       = "CheckProperties"
	CmdGetStats              = "GetStats"
	CmdShutdown              = "Shutdown"
)

// StepRequest is the data for Step command.
type StepRequest struct {
	Steps   uint64 `json:"steps"`
	DeltaNS int64  `json:"delta_ns"`
}

// StepResponse is the response for Step command.
type StepResponse struct {
	StepCount     uint64           `json:"step_count"`
	VirtualTimeNS int64            `json:"virtual_time_ns"`
	Faults        []Fault          `json:"faults,omitempty"`
	Properties    map[string]CheckResult `json:"properties,omitempty"`
	Running       bool             `json:"running"`
}

// StateResponse is the response for GetState command.
type StateResponse struct {
	Running        bool   `json:"running"`
	Seed           uint64 `json:"seed"`
	StepCount      uint64 `json:"step_count"`
	VirtualTimeNS  int64  `json:"virtual_time_ns"`
	FaultsInjected uint64 `json:"faults_injected"`
	SnapshotCount  int    `json:"snapshot_count"`
}

// SnapshotRequest is the data for Snapshot command.
type SnapshotRequest struct {
	ID          string `json:"id,omitempty"`
	Description string `json:"description,omitempty"`
}

// SnapshotResponse is the response for Snapshot command.
type SnapshotResponse struct {
	ID SnapshotID `json:"id"`
}

// RestoreRequest is the data for Restore command.
type RestoreRequest struct {
	ID SnapshotID `json:"id"`
}

// ScheduleFaultRequest is the data for ScheduleFault command.
type ScheduleFaultRequest struct {
	TriggerTimeNS int64 `json:"trigger_time_ns"`
	Fault         Fault `json:"fault"`
}

// ScheduleFaultResponse is the response for ScheduleFault command.
type ScheduleFaultResponse struct {
	FaultID uint64 `json:"fault_id"`
}

// CancelFaultRequest is the data for CancelFault command.
type CancelFaultRequest struct {
	FaultID uint64 `json:"fault_id"`
}

// StatsResponse is the response for GetStats command.
type StatsResponse struct {
	FaultStats FaultStats `json:"fault_stats"`
}

// ControlServer handles communication with Bloodhound over a Unix socket.
type ControlServer struct {
	socketPath  string
	coordinator *SimulationCoordinator
	listener    net.Listener
	done        chan struct{}
	mu          sync.Mutex
}

// NewControlServer creates a new control server.
func NewControlServer(socketPath string, coordinator *SimulationCoordinator) *ControlServer {
	return &ControlServer{
		socketPath:  socketPath,
		coordinator: coordinator,
		done:        make(chan struct{}),
	}
}

// Start starts the control server.
func (s *ControlServer) Start() error {
	// Remove existing socket file.
	os.Remove(s.socketPath)

	listener, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.socketPath, err)
	}

	s.listener = listener
	log.Infof("DST control server listening on %s", s.socketPath)

	go s.acceptLoop()
	return nil
}

// Stop stops the control server.
func (s *ControlServer) Stop() {
	close(s.done)
	if s.listener != nil {
		s.listener.Close()
	}
	os.Remove(s.socketPath)
}

func (s *ControlServer) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
				log.Warningf("DST control server accept error: %v", err)
				continue
			}
		}

		go s.handleConnection(conn)
	}
}

func (s *ControlServer) handleConnection(conn net.Conn) {
	defer conn.Close()

	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)

	for {
		var cmd ControlCommand
		if err := decoder.Decode(&cmd); err != nil {
			if err != io.EOF {
				log.Warningf("DST control: decode error: %v", err)
			}
			return
		}

		response := s.handleCommand(cmd)
		if err := encoder.Encode(response); err != nil {
			log.Warningf("DST control: encode error: %v", err)
			return
		}

		// Handle shutdown command.
		if cmd.Type == CmdShutdown {
			return
		}
	}
}

func (s *ControlServer) handleCommand(cmd ControlCommand) ControlResponse {
	switch cmd.Type {
	case CmdStep:
		return s.handleStep(cmd.Data)
	case CmdPause:
		return s.handlePause()
	case CmdResume:
		return s.handleResume()
	case CmdGetState:
		return s.handleGetState()
	case CmdSnapshot:
		return s.handleSnapshot(cmd.Data)
	case CmdRestore:
		return s.handleRestore(cmd.Data)
	case CmdSetFaultProbabilities:
		return s.handleSetFaultProbabilities(cmd.Data)
	case CmdScheduleFault:
		return s.handleScheduleFault(cmd.Data)
	case CmdCancelFault:
		return s.handleCancelFault(cmd.Data)
	case CmdGetStats:
		return s.handleGetStats()
	case CmdShutdown:
		return ControlResponse{Status: "ok"}
	default:
		return ControlResponse{
			Status: "error",
			Error:  fmt.Sprintf("unknown command: %s", cmd.Type),
		}
	}
}

func (s *ControlServer) handleStep(data json.RawMessage) ControlResponse {
	var req StepRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return ControlResponse{Status: "error", Error: err.Error()}
	}

	if req.Steps == 0 {
		req.Steps = 1
	}
	if req.DeltaNS == 0 {
		req.DeltaNS = 1000000 // 1ms default
	}

	var allFaults []Fault
	var lastProperties map[string]CheckResult
	var running bool

	for i := uint64(0); i < req.Steps; i++ {
		faults, props, r := s.coordinator.Step(req.DeltaNS)
		allFaults = append(allFaults, faults...)
		lastProperties = props
		running = r
		if !running {
			break
		}
	}

	resp := StepResponse{
		StepCount:     s.coordinator.GetStepCount(),
		VirtualTimeNS: s.coordinator.GetCurrentTime(),
		Faults:        allFaults,
		Properties:    lastProperties,
		Running:       running,
	}

	respData, _ := json.Marshal(resp)
	return ControlResponse{Status: "ok", Data: respData}
}

func (s *ControlServer) handlePause() ControlResponse {
	s.coordinator.Stop("pause requested")
	return ControlResponse{Status: "ok"}
}

func (s *ControlServer) handleResume() ControlResponse {
	s.coordinator.Start()
	return ControlResponse{Status: "ok"}
}

func (s *ControlServer) handleGetState() ControlResponse {
	tree := s.coordinator.GetSnapshotTree()
	fi := s.coordinator.GetFaultInjector()

	resp := StateResponse{
		Running:        s.coordinator.IsRunning(),
		StepCount:      s.coordinator.GetStepCount(),
		VirtualTimeNS:  s.coordinator.GetCurrentTime(),
		FaultsInjected: fi.GetStats().FaultsInjected,
		SnapshotCount:  tree.SnapshotCount(),
	}

	respData, _ := json.Marshal(resp)
	return ControlResponse{Status: "ok", Data: respData}
}

func (s *ControlServer) handleSnapshot(data json.RawMessage) ControlResponse {
	var req SnapshotRequest
	if data != nil {
		json.Unmarshal(data, &req)
	}

	tree := s.coordinator.GetSnapshotTree()
	state := &DSTState{
		Metadata: SnapshotMetadata{
			Seed:          0, // TODO: get from coordinator
			VirtualTimeNS: s.coordinator.GetCurrentTime(),
			StepCount:     s.coordinator.GetStepCount(),
			Description:   req.Description,
		},
	}

	id, err := tree.CreateSnapshot(state, nil)
	if err != nil {
		return ControlResponse{Status: "error", Error: err.Error()}
	}

	resp := SnapshotResponse{ID: id}
	respData, _ := json.Marshal(resp)
	return ControlResponse{Status: "ok", Data: respData}
}

func (s *ControlServer) handleRestore(data json.RawMessage) ControlResponse {
	var req RestoreRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return ControlResponse{Status: "error", Error: err.Error()}
	}

	if err := s.coordinator.RestoreToSnapshot(req.ID); err != nil {
		return ControlResponse{Status: "error", Error: err.Error()}
	}

	return ControlResponse{Status: "ok"}
}

func (s *ControlServer) handleSetFaultProbabilities(data json.RawMessage) ControlResponse {
	var probs FaultProbabilities
	if err := json.Unmarshal(data, &probs); err != nil {
		return ControlResponse{Status: "error", Error: err.Error()}
	}

	s.coordinator.GetFaultInjector().SetProbabilities(probs)
	return ControlResponse{Status: "ok"}
}

func (s *ControlServer) handleScheduleFault(data json.RawMessage) ControlResponse {
	var req ScheduleFaultRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return ControlResponse{Status: "error", Error: err.Error()}
	}

	id := s.coordinator.GetFaultInjector().Schedule(req.TriggerTimeNS, req.Fault)

	resp := ScheduleFaultResponse{FaultID: id}
	respData, _ := json.Marshal(resp)
	return ControlResponse{Status: "ok", Data: respData}
}

func (s *ControlServer) handleCancelFault(data json.RawMessage) ControlResponse {
	var req CancelFaultRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return ControlResponse{Status: "error", Error: err.Error()}
	}

	if s.coordinator.GetFaultInjector().CancelScheduled(req.FaultID) {
		return ControlResponse{Status: "ok"}
	}
	return ControlResponse{Status: "error", Error: "fault not found"}
}

func (s *ControlServer) handleGetStats() ControlResponse {
	stats := s.coordinator.GetFaultInjector().GetStats()
	resp := StatsResponse{FaultStats: stats}
	respData, _ := json.Marshal(resp)
	return ControlResponse{Status: "ok", Data: respData}
}

// Global control server instance.
var globalControlServer struct {
	mu     sync.Mutex
	server *ControlServer
}

// StartGlobalControlServer starts the global control server.
func StartGlobalControlServer(socketPath string) error {
	globalControlServer.mu.Lock()
	defer globalControlServer.mu.Unlock()

	coord := GetGlobalCoordinator()
	if coord == nil {
		return fmt.Errorf("global coordinator not initialized")
	}

	server := NewControlServer(socketPath, coord)
	if err := server.Start(); err != nil {
		return err
	}

	globalControlServer.server = server
	return nil
}

// StopGlobalControlServer stops the global control server.
func StopGlobalControlServer() {
	globalControlServer.mu.Lock()
	defer globalControlServer.mu.Unlock()

	if globalControlServer.server != nil {
		globalControlServer.server.Stop()
		globalControlServer.server = nil
	}
}
