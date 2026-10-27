package service

import (
	"testing"
	"time"
)

func TestChannelOperationsMutationBoundaries(t *testing.T) {
	revision := int64(0)
	tests := []struct {
		name  string
		m     ChannelOperationsMutation
		valid bool
	}{
		{"missing version", ChannelOperationsMutation{Action: "create_upstream", Name: "Provider"}, false},
		{"blank name", ChannelOperationsMutation{Action: "create_upstream", Name: " \t ", Revision: &revision}, false},
		{"control name", ChannelOperationsMutation{Action: "create_upstream", Name: "bad\nname", Revision: &revision}, false},
		{"create", ChannelOperationsMutation{Action: "create_upstream", Name: " Provider ", Revision: &revision}, true},
		{"rename missing id", ChannelOperationsMutation{Action: "update_upstream", Name: "Provider", Revision: &revision}, false},
		{"delete unconfirmed", ChannelOperationsMutation{Action: "delete_upstream", UpstreamID: 1, Revision: &revision}, false},
		{"delete confirmed", ChannelOperationsMutation{Action: "delete_upstream", UpstreamID: 1, Revision: &revision, Confirm: true}, true},
		{"initial assign", ChannelOperationsMutation{Action: "assign", AccountID: 1, UpstreamID: 2, Revision: &revision}, true},
		{"reassign unconfirmed", ChannelOperationsMutation{Action: "assign", AccountID: 1, UpstreamID: 2, ExpectedUpstreamID: 3, Revision: &revision}, false},
		{"reassign confirmed", ChannelOperationsMutation{Action: "assign", AccountID: 1, UpstreamID: 2, ExpectedUpstreamID: 3, Revision: &revision, Confirm: true}, true},
		{"detach missing previous", ChannelOperationsMutation{Action: "detach", AccountID: 1, Revision: &revision, Confirm: true}, false},
		{"detach unconfirmed", ChannelOperationsMutation{Action: "detach", AccountID: 1, ExpectedUpstreamID: 2, Revision: &revision}, false},
		{"detach", ChannelOperationsMutation{Action: "detach", AccountID: 1, ExpectedUpstreamID: 2, Revision: &revision, Confirm: true}, true},
		{"unknown action", ChannelOperationsMutation{Action: "probe", Revision: &revision}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateChannelOperationsMutation(&tt.m)
			if (err == nil) != tt.valid {
				t.Fatalf("valid=%v err=%v", tt.valid, err)
			}
		})
	}
}
func TestChannelOperationsFilterBoundaries(t *testing.T) {
	base := ChannelOperationsFilter{From: time.Now().Add(-time.Hour), Until: time.Now(), ModelScope: "public", Dimension: "channel", Page: 1, PageSize: 20}
	if err := ValidateChannelOperationsFilter(base); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*ChannelOperationsFilter){
		func(f *ChannelOperationsFilter) { f.Until = f.From }, func(f *ChannelOperationsFilter) { f.From = f.Until.Add(-32 * 24 * time.Hour) },
		func(f *ChannelOperationsFilter) { f.ModelScope = "routing" }, func(f *ChannelOperationsFilter) { f.Dimension = "endpoint" },
		func(f *ChannelOperationsFilter) { f.UpstreamID = 7 }, func(f *ChannelOperationsFilter) { f.PageSize = 101 },
	} {
		f := base
		mutate(&f)
		if ValidateChannelOperationsFilter(f) == nil {
			t.Fatalf("accepted invalid filter: %+v", f)
		}
	}
}
