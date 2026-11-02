package service

import (
	"sort"
	"strings"
)

// UserGroupInflightLoad is the per-group slice of a user's in-flight requests.
// It is derived from the Redis snapshots the gateway already writes for the
// admin usage page, so no extra database query or new counter is needed.
type UserGroupInflightLoad struct {
	GroupID   int64    `json:"group_id"`
	GroupName string   `json:"group_name,omitempty"`
	Inflight  int      `json:"inflight"`
	Models    []string `json:"models,omitempty"`
}

// SummarizeUserGroupLoad groups in-flight snapshots by group and counts them.
// Rows without a group id are kept under group 0 so the caller can show
// "not selected yet" instead of silently dropping active work.
func SummarizeUserGroupLoad(rows []UsageInflightSnapshot) []UserGroupInflightLoad {
	if len(rows) == 0 {
		return []UserGroupInflightLoad{}
	}

	type accumulator struct {
		groupID   int64
		groupName string
		inflight  int
		models    map[string]struct{}
	}
	byGroup := make(map[int64]*accumulator, len(rows))
	for _, row := range rows {
		groupID := row.GroupID
		item := byGroup[groupID]
		if item == nil {
			item = &accumulator{groupID: groupID, models: make(map[string]struct{})}
			byGroup[groupID] = item
		}
		item.inflight++
		if item.groupName == "" {
			item.groupName = strings.TrimSpace(row.GroupName)
		}
		if model := strings.TrimSpace(row.Model); model != "" {
			item.models[model] = struct{}{}
		}
	}

	out := make([]UserGroupInflightLoad, 0, len(byGroup))
	for _, item := range byGroup {
		models := make([]string, 0, len(item.models))
		for model := range item.models {
			models = append(models, model)
		}
		sort.Strings(models)
		out = append(out, UserGroupInflightLoad{
			GroupID:   item.groupID,
			GroupName: item.groupName,
			Inflight:  item.inflight,
			Models:    models,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Inflight == out[j].Inflight {
			if out[i].GroupID == 0 {
				return false
			}
			if out[j].GroupID == 0 {
				return true
			}
			return out[i].GroupID < out[j].GroupID
		}
		return out[i].Inflight > out[j].Inflight
	})
	return out
}
