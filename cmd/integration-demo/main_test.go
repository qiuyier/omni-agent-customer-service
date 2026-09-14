package main

import (
	"github.com/qiuyier/omni-agent-customer-service/internal/integration"
	"testing"
)

func TestOfflineDemo(t *testing.T) {
	result, err := run()
	if err != nil {
		t.Fatal(err)
	}
	if result.ModelCalls != 2 || result.Deliveries != 2 || result.LostAcknowledgements != 3 || len(result.States) != 2 {
		t.Fatalf("%+v", result)
	}
	for _, state := range result.States {
		if state != integration.Completed {
			t.Fatal(state)
		}
	}
}
