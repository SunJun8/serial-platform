package server

import (
	"testing"
	"time"

	"serial-platform/internal/protocol"
)

func TestTerminalOperationRegistryRejectsDuplicateRequestID(t *testing.T) {
	registry := newTerminalOperationRegistry()
	connection := agentConnectionToken("connection-1")
	first, err := registry.register("agent-1", connection, "request-1")
	if err != nil {
		t.Fatalf("first register returned error: %v", err)
	}
	if first == nil {
		t.Fatal("first register returned nil result channel")
	}

	second, err := registry.register("agent-1", connection, "request-1")
	if err == nil {
		t.Fatal("second register returned nil error, want duplicate request error")
	}
	if second != nil {
		t.Fatalf("second register channel = %v, want nil", second)
	}
}

func TestTerminalOperationRegistryBindsResultsToAgent(t *testing.T) {
	registry := newTerminalOperationRegistry()
	connection := agentConnectionToken("connection-1")
	resultCh, err := registry.register("agent-1", connection, "request-1")
	if err != nil {
		t.Fatalf("register returned error: %v", err)
	}

	if registry.complete("agent-2", connection, protocol.OperationResult{
		Type:      protocol.MessageOperationResult,
		RequestID: "request-1",
		OK:        true,
	}) {
		t.Fatal("complete returned true for wrong agent")
	}
	select {
	case result := <-resultCh:
		t.Fatalf("wrong-agent result completed pending request: %+v", result)
	default:
	}

	if !registry.complete("agent-1", connection, protocol.OperationResult{
		Type:      protocol.MessageOperationResult,
		RequestID: "request-1",
		OK:        true,
	}) {
		t.Fatal("complete returned false for matching agent")
	}
	select {
	case result := <-resultCh:
		if !result.OK || result.RequestID != "request-1" {
			t.Fatalf("result = %+v, want matching OK result", result)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for matching result")
	}
}

func TestTerminalOperationRegistryBindsResultsToConnection(t *testing.T) {
	registry := newTerminalOperationRegistry()
	oldConnection := agentConnectionToken("old-connection")
	newConnection := agentConnectionToken("new-connection")
	resultCh, err := registry.register("agent-1", newConnection, "request-1")
	if err != nil {
		t.Fatalf("register returned error: %v", err)
	}

	registry.failConnection("agent-1", oldConnection, errAgentNotConnected)
	select {
	case result := <-resultCh:
		t.Fatalf("old connection failure completed new pending request: %+v", result)
	default:
	}

	if registry.complete("agent-1", oldConnection, protocol.OperationResult{
		Type:      protocol.MessageOperationResult,
		RequestID: "request-1",
		OK:        true,
	}) {
		t.Fatal("complete returned true for old connection")
	}
	select {
	case result := <-resultCh:
		t.Fatalf("old connection result completed new pending request: %+v", result)
	default:
	}

	if !registry.complete("agent-1", newConnection, protocol.OperationResult{
		Type:      protocol.MessageOperationResult,
		RequestID: "request-1",
		OK:        true,
	}) {
		t.Fatal("complete returned false for matching connection")
	}
	select {
	case result := <-resultCh:
		if !result.OK || result.RequestID != "request-1" {
			t.Fatalf("result = %+v, want matching OK result", result)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for matching connection result")
	}
}
