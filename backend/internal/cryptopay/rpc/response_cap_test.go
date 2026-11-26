package rpc

import "testing"

// The cap is a decision, not an accident: raising it lets a node make this process
// allocate more per call; lowering it fails answers that are fine today.
func TestAnswerSizeCapIsEightMiB(t *testing.T) {
	if maxResponseBytes != 8<<20 {
		t.Fatalf("maxResponseBytes = %d, want 8 MiB; change this test together with the cap on purpose", maxResponseBytes)
	}
	if maxDrainBytes != 64<<10 {
		t.Fatalf("maxDrainBytes = %d, want 64 KiB", maxDrainBytes)
	}
}
