package github

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func rev(login, state string) Review {
	var r Review
	r.User.Login = login
	r.State = state
	return r
}

func TestReviewDecision(t *testing.T) {
	tests := []struct {
		name    string
		reviews []Review
		want    string
	}{
		{"no reviews", nil, ReviewNone},
		{"only comments", []Review{rev("a", "COMMENTED")}, ReviewNone},
		{"approved", []Review{rev("a", "APPROVED")}, ReviewApproved},
		{"a comment after approving keeps the approval", []Review{rev("a", "APPROVED"), rev("a", "COMMENTED")}, ReviewApproved},
		{"changes requested wins over another approval", []Review{rev("a", "APPROVED"), rev("b", "CHANGES_REQUESTED")}, ReviewChangesRequested},
		{"approving after requesting changes", []Review{rev("b", "CHANGES_REQUESTED"), rev("b", "APPROVED")}, ReviewApproved},
		{"dismissed", []Review{rev("b", "CHANGES_REQUESTED"), rev("b", "DISMISSED")}, ReviewNone},
		{"logins are case-insensitive", []Review{rev("Bob", "CHANGES_REQUESTED"), rev("bob", "APPROVED")}, ReviewApproved},
		{"pending reviews don't count", []Review{rev("a", "PENDING")}, ReviewNone},
	}
	for _, tt := range tests {
		if got := ReviewDecision(tt.reviews); got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestMergeStatus(t *testing.T) {
	for state, want := range map[string]string{
		"clean": MergeReady, "has_hooks": MergeReady, "dirty": MergeConflict, "blocked": MergeBlocked,
		"behind": MergeBehind, "unstable": MergeUnstable, "draft": MergeDraft, "unknown": "", "": "",
	} {
		if got := MergeStatus(&PullRequest{MergeableState: state}); got != want {
			t.Errorf("MergeStatus(%q) = %q, want %q", state, got, want)
		}
	}
}

func TestGetReviews_ReadsPastThreeHundredReviews(t *testing.T) {
	// 350 comments, then a change request on page 4: the newest verdict must count.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var page int
		fmt.Sscan(r.URL.Query().Get("page"), &page)
		count := reviewsPerPage
		if page == 4 {
			count = 51
		}
		parts := make([]string, count)
		for i := range parts {
			parts[i] = `{"user":{"login":"u"},"state":"COMMENTED"}`
		}
		if page == 1 {
			parts[0] = `{"user":{"login":"a"},"state":"APPROVED"}`
		}
		if page == 4 {
			parts[50] = `{"user":{"login":"b"},"state":"CHANGES_REQUESTED"}`
		}
		fmt.Fprint(w, "["+strings.Join(parts, ",")+"]")
	}))
	defer srv.Close()
	c := NewClient("token")
	c.BaseURL = srv.URL

	reviews, err := c.GetReviews(context.Background(), "o", "r", 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(reviews) != 351 || ReviewDecision(reviews) != ReviewChangesRequested {
		t.Errorf("got %d reviews, decision %q; want 351 and the change request on page 4", len(reviews), ReviewDecision(reviews))
	}
}

func TestGetReviews_Paginates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/repos/o/r/pulls/7/reviews") {
			http.NotFound(w, r)
			return
		}
		count := reviewsPerPage
		if r.URL.Query().Get("page") == "2" {
			count = 1
		}
		parts := make([]string, count)
		for i := range parts {
			parts[i] = fmt.Sprintf(`{"id":%d,"user":{"login":"u"},"state":"COMMENTED"}`, i)
		}
		if r.URL.Query().Get("page") == "2" {
			parts[0] = `{"id":999,"user":{"login":"u"},"state":"APPROVED"}`
		}
		fmt.Fprint(w, "["+strings.Join(parts, ",")+"]")
	}))
	defer srv.Close()
	c := NewClient("token")
	c.BaseURL = srv.URL

	reviews, err := c.GetReviews(context.Background(), "o", "r", 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(reviews) != reviewsPerPage+1 || ReviewDecision(reviews) != ReviewApproved {
		t.Errorf("got %d reviews, decision %q; want the approval on page 2 to count", len(reviews), ReviewDecision(reviews))
	}
}
