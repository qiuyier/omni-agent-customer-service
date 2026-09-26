package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Replay actual outputs, not another model call or a repaired fixture. Valid
// JSON does not imply a valid contract, evidence, or semantic classification.
func TestArchivedInventoryContracts(t *testing.T) {
	for _, tc := range []struct {
		file, stage, want string
	}{
		{"3.json", "primary", "assessment_for_non_fact"},
		{"8.json", "primary", "assessment_for_non_fact"},
		{"13.json", "review", "invalid_inventory_review"},
		{"17.json", "review", "invalid_inventory_evidence"},
		{"9.json", "primary", ""},
		{"9.json", "review", ""},
	} {
		t.Run(tc.file+"/"+tc.stage, func(t *testing.T) {
			path := filepath.Join("..", "..", "examples", "knowledge", "webchat", "eval_runs", "2026-09-24", "inventory-v1-repeated", tc.file)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var archived struct {
				Samples []judgeInput `json:"samples"`
				Results []result     `json:"results"`
			}
			if err := json.Unmarshal(data, &archived); err != nil {
				t.Fatal(err)
			}
			if len(archived.Samples) != 1 || len(archived.Results) != 1 {
				t.Fatal("expected frozen single-case report")
			}
			raw := archived.Results[0].TypedPrimaryRaw
			if tc.stage == "review" {
				raw = archived.Results[0].InventoryReviewRaw
			}
			if !json.Valid([]byte(raw)) {
				t.Fatal("fixture must be syntactically valid JSON")
			}
			if tc.stage == "primary" {
				_, _, err = parseTyped(raw, archived.Samples[0])
			} else {
				_, err = parseInventory(raw, archived.Samples[0])
			}
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || err.Error() != tc.want {
				t.Fatalf("want %s, got %v", tc.want, err)
			}
		})
	}
}
