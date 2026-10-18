package github

import (
	"encoding/json"
	"testing"
	"time"
)

func TestApplyNoCI(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		ci        CIStatus
		updatedAt time.Time
		want      string
	}{
		{"no checks, quiet past the grace period", CIStatus{State: "pending"}, now.Add(-NoCIGracePeriod), StateNoCI},
		{"no checks, quiet for days", CIStatus{State: "pending"}, now.Add(-72 * time.Hour), StateNoCI},
		{"no checks yet, just pushed", CIStatus{State: "pending"}, now.Add(-30 * time.Second), "pending"},
		{"no checks, just short of the grace period", CIStatus{State: "pending"}, now.Add(-NoCIGracePeriod + time.Second), "pending"},
		{"no checks, unknown update time", CIStatus{State: "pending"}, time.Time{}, "pending"},
		{"checks still running", CIStatus{State: "pending", Total: 2, Pending: 1}, now.Add(-time.Hour), "pending"},
		{"success is left alone", CIStatus{State: "success", Total: 1}, now.Add(-time.Hour), "success"},
		{"failure is left alone", CIStatus{State: "failure", Total: 1}, now.Add(-time.Hour), "failure"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ci := tt.ci
			ApplyNoCI(&ci, tt.updatedAt, now)
			if ci.State != tt.want {
				t.Errorf("state = %q, want %q", ci.State, tt.want)
			}
		})
	}
}

func TestApplyNoCI_WithSummarizeCI(t *testing.T) {
	now := time.Now()
	old := now.Add(-time.Hour)

	// The shape the real API returns for a commit nothing reported on.
	none := SummarizeCI(&CombinedStatus{State: "pending", TotalCount: 0}, nil)
	ApplyNoCI(none, old, now)
	if none.State != StateNoCI {
		t.Errorf("empty CI = %q, want no_ci", none.State)
	}

	// A legacy status that is still pending is a real signal, not "no CI".
	legacy := SummarizeCI(&CombinedStatus{State: "pending", TotalCount: 1, Statuses: []CommitStatus{{Context: "ci", State: "pending"}}}, nil)
	ApplyNoCI(legacy, old, now)
	if legacy.State != "pending" {
		t.Errorf("pending legacy status = %q, want pending", legacy.State)
	}
}

func TestPullRequest_DecodesUpdatedAt(t *testing.T) {
	var pr PullRequest
	if err := json.Unmarshal([]byte(`{"number":7,"updated_at":"2026-10-18T03:27:41Z"}`), &pr); err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 10, 18, 3, 27, 41, 0, time.UTC); !pr.UpdatedAt.Equal(want) {
		t.Errorf("UpdatedAt = %v, want %v", pr.UpdatedAt, want)
	}
}
