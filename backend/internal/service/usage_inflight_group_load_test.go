package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSummarizeUserGroupLoadGroupsAndSorts(t *testing.T) {
	rows := []UsageInflightSnapshot{
		{RequestID: "a", GroupID: 7, GroupName: "gpt", Model: "gpt-5"},
		{RequestID: "b", GroupID: 7, GroupName: "gpt", Model: "gpt-5-mini"},
		{RequestID: "c", GroupID: 7, GroupName: "gpt", Model: "gpt-5"},
		{RequestID: "d", GroupID: 9, GroupName: "claude", Model: "claude-opus"},
		{RequestID: "e", GroupID: 0, Model: "unknown"},
	}
	got := SummarizeUserGroupLoad(rows)
	require.Len(t, got, 3)
	require.Equal(t, int64(7), got[0].GroupID)
	require.Equal(t, 3, got[0].Inflight)
	require.Equal(t, "gpt", got[0].GroupName)
	require.Equal(t, []string{"gpt-5", "gpt-5-mini"}, got[0].Models)
	require.Equal(t, int64(9), got[1].GroupID)
	require.Equal(t, 1, got[1].Inflight)
	require.Equal(t, int64(0), got[2].GroupID)
	require.Equal(t, 1, got[2].Inflight)
}

func TestSummarizeUserGroupLoadEmpty(t *testing.T) {
	require.Empty(t, SummarizeUserGroupLoad(nil))
}
