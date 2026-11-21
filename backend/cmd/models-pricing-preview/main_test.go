package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestModelsPricingPreviewCommandJSON(t *testing.T) {
	var out, stderr bytes.Buffer
	args := []string{"-snapshot", "fixture-snapshot.json", "-inventory", "fixture-inventory.json"}
	if code := run(args, &out, &stderr); code != 0 {
		t.Fatalf("exited %d: %s", code, stderr.String())
	}
	var report struct {
		SnapshotSHA256 string         `json:"snapshot_sha256"`
		Summary        map[string]int `json:"summary"`
		Rows           []struct {
			RowID    string `json:"row_id"`
			InputRow int    `json:"input_row"`
			Status   string `json:"status"`
			Fields   []struct {
				Quote *json.Number `json:"quote"`
			} `json:"fields"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("stdout is not a JSON report: %v\n%s", err, out.String())
	}
	snapshot, err := os.ReadFile(args[1])
	if err != nil {
		t.Fatal(err)
	}
	if report.SnapshotSHA256 != fmt.Sprintf("%x", sha256.Sum256(snapshot)) || report.Summary["missing"] != 1 || report.Summary["comparable"] != 1 {
		t.Errorf("wrong hash or summary: %s", out.String())
	}
	if len(report.Rows) != 2 || report.Rows[0].InputRow != 1 || report.Rows[0].RowID != "google-flat" || report.Rows[0].Status != "comparable" ||
		report.Rows[1].InputRow != 2 || report.Rows[1].RowID != "missing-neon" || report.Rows[1].Status != "missing" {
		t.Fatalf("wrong input row mapping: %s", out.String())
	}
	if fields := report.Rows[0].Fields; len(fields) != 2 || fields[1].Quote == nil || fields[1].Quote.String() != "0" {
		t.Errorf("explicit zero did not survive JSON: %s", out.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("unexpected stderr: %s", stderr.String())
	}
}

func TestModelsPricingPreviewCommandErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		code int
		want string
	}{
		{"missing flags", nil, 2, "usage:"},
		{"unexpected argument", []string{"-snapshot", "a", "-inventory", "b", "extra"}, 2, "usage:"},
		{"missing file", []string{"-snapshot", "missing.json", "-inventory", "fixture-inventory.json"}, 1, "read snapshot"},
		{"bad snapshot", []string{"-snapshot", "fixture-inventory.json", "-inventory", "fixture-inventory.json"}, 1, "snapshot"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, stderr bytes.Buffer
			if code := run(tc.args, &out, &stderr); code != tc.code || out.Len() != 0 || !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("exit=%d, stdout=%q, stderr=%q", code, out.String(), stderr.String())
			}
		})
	}
}
