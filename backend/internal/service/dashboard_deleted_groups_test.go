package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
)

// Usage whose group row is gone must still be reported - with a name and a
// marker - otherwise the chart silently drops it and the visible rows stop
// adding up to the real request count.
func TestNormalizeDeletedGroupsMarksUnnamedRows(t *testing.T) {
	stats := []usagestats.GroupStat{
		{GroupID: 7, GroupName: "GPT 不降智", Requests: 10},
		{GroupID: 42, GroupName: "", Requests: 3},
	}

	got := normalizeDeletedGroups(stats)

	if got[0].Deleted {
		t.Fatalf("live group must not be marked deleted")
	}
	if got[0].GroupName != "GPT 不降智" {
		t.Fatalf("live group name changed: %q", got[0].GroupName)
	}
	if !got[1].Deleted {
		t.Fatalf("unnamed group must be marked deleted")
	}
	if got[1].GroupName != "已删除分组 #42" {
		t.Fatalf("deleted group name = %q", got[1].GroupName)
	}
	if got[1].Requests != 3 {
		t.Fatalf("requests must be preserved, got %d", got[1].Requests)
	}
}

// The "no group" bucket (group_id = 0) belongs to key-less traffic, not to a
// deleted group, and must keep rendering as before.
func TestNormalizeDeletedGroupsLeavesNoGroupBucketAlone(t *testing.T) {
	got := normalizeDeletedGroups([]usagestats.GroupStat{{GroupID: 0, GroupName: "", Requests: 5}})

	if got[0].Deleted {
		t.Fatalf("group_id=0 must not be treated as a deleted group")
	}
	if got[0].GroupName != "" {
		t.Fatalf("group_id=0 name must stay empty, got %q", got[0].GroupName)
	}
}
