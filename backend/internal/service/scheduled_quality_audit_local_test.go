package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestScheduledQualityAuditLocal(t *testing.T) {
	dir := os.Getenv("SCHEDULED_QUALITY_SAMPLE_DIR")
	if dir == "" {
		t.Skip("local")
	}
	files, e := filepath.Glob(filepath.Join(dir, "a29173-*.html"))
	if e != nil {
		t.Fatal(e)
	}
	var out []map[string]string
	for _, f := range files {
		b, e := os.ReadFile(f)
		if e != nil {
			t.Fatal(e)
		}
		status, reason := assessScheduledTestQuality(string(b), "")
		out = append(out, map[string]string{"file": filepath.Base(f), "status": status, "reason": reason})
		t.Log(filepath.Base(f), status, reason)
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	if e := os.WriteFile(filepath.Join(dir, "account-29173-classification.json"), b, 0600); e != nil {
		t.Fatal(e)
	}
}
