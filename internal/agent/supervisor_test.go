package agent_test

import (
	"testing"

	"serial-platform/internal/agent"
	"serial-platform/internal/serial"
)

func TestSupervisorAddsChannelWorker(t *testing.T) {
	supervisor := agent.NewSupervisor()
	worker := serial.NewWorker("channel-1", serial.DefaultConfig(), serial.NewFakeBackend())

	if err := supervisor.AddChannel("channel-1", worker); err != nil {
		t.Fatalf("AddChannel returned error: %v", err)
	}

	control, ok := supervisor.Channel("channel-1")
	if !ok {
		t.Fatal("Channel did not find channel-1")
	}
	if control != worker {
		t.Fatal("Channel returned a different worker")
	}

	if err := supervisor.AddChannel("channel-1", worker); err == nil {
		t.Fatal("duplicate AddChannel returned nil error")
	}
}
