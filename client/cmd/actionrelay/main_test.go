package main

import (
	"testing"

	"actionrelay/client/internal/agent"
)

func TestBuildDiagnosticsCriticalWhenAgentUnreachable(t *testing.T) {
	diag := buildDiagnostics(statusOutput{
		Agent: statusAgent{
			Reachable: false,
		},
	})
	if diag.Severity != "critical" {
		t.Fatalf("expected critical severity, got %q", diag.Severity)
	}
}

func TestBuildDiagnosticsWarningForBackpressure(t *testing.T) {
	diag := buildDiagnostics(statusOutput{
		Agent: statusAgent{
			Reachable: true,
			Runtime: &agent.Snapshot{
				QueueCapacity:      100,
				QueueDepth:         10,
				BackpressureActive: true,
			},
		},
	})
	if diag.Severity != "warning" {
		t.Fatalf("expected warning severity, got %q", diag.Severity)
	}
}

func TestBuildDiagnosticsWarningForHighBatchLatency(t *testing.T) {
	diag := buildDiagnostics(statusOutput{
		Agent: statusAgent{
			Reachable: true,
			Runtime: &agent.Snapshot{
				QueueCapacity:      100,
				QueueDepth:         10,
				LastBatchLatencyMS: 180000,
			},
		},
	})
	if diag.Severity != "warning" {
		t.Fatalf("expected warning severity, got %q", diag.Severity)
	}
}
