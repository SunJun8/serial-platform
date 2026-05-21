package server

import (
	"testing"
	"time"

	"serial-platform/internal/protocol"
)

func TestTerminalOperationRegistryRejectsDuplicateRequestID(t *testing.T) {
	registry := newTerminalOperationRegistry()
	first, err := registry.register("agent-1", "request-1")
	if err != nil {
		t.Fatalf("first register returned error: %v", err)
	}
	if first == nil {
		t.Fatal("first register returned nil result channel")
	}

	second, err := registry.register("agent-1", "request-1")
	if err == nil {
		t.Fatal("second register returned nil error, want duplicate request error")
	}
	if second != nil {
		t.Fatalf("second register channel = %v, want nil", second)
	}
}

func TestTerminalOperationRegistryBindsResultsToAgent(t *testing.T) {
	registry := newTerminalOperationRegistry()
	resultCh, err := registry.register("agent-1", "request-1")
	if err != nil {
		t.Fatalf("register returned error: %v", err)
	}

	if registry.complete("agent-2", protocol.OperationResult{
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

	if !registry.complete("agent-1", protocol.OperationResult{
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
