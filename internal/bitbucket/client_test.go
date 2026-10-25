package bitbucket

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/devblac/prw/internal/github"
)

const prJSON = `{
  "id": 42, "title": "Add login", "state": %q, "updated_on": "2026-10-24T10:00:00.123456+00:00",
  "source": {"commit": {"hash": "abc123def456"}},
  "participants": [
    {"user": {"uuid": "{a}", "display_name": "Ana"}, "role": "REVIEWER", "approved": true, "state": "approved"},
    {"user": {"uuid": "{b}", "display_name": "Bob"}, "role": "REVIEWER", "approved": false, "state": "changes_requested"},
    {"user": {"uuid": "{c}", "display_name": "Cid"}, "role": "PARTICIPANT", "approved": false, "state": null}
  ]
}`

type fakeBitbucket struct {
	*httptest.Server
	prState     string
	mergeChecks string
	statuses    []string // pages
	requests    map[string]*int32
	lastAuth    string
}

func newFake(t *testing.T) *fakeBitbucket {
	f := &fakeBitbucket{prState: "OPEN", requests: map[string]*int32{}}
	for _, k := range []string{"pr", "merge", "statuses"} {
		f.requests[k] = new(int32)
	}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.lastAuth = r.Header.Get("Authorization")
		p := r.URL.Path
		switch {
		case strings.HasSuffix(p, "/repositories/acme/api/pullrequests/42"):
			atomic.AddInt32(f.requests["pr"], 1)
			fmt.Fprintf(w, prJSON, f.prState)
		case strings.HasSuffix(p, "/pullrequests/42/mergeability/checks"):
			atomic.AddInt32(f.requests["merge"], 1)
			if f.mergeChecks == "" {
				http.NotFound(w, r)
				return
			}
			fmt.Fprintf(w, `{"values": [%s]}`, f.mergeChecks)
		case strings.HasSuffix(p, "/commit/abc123def456/statuses"):
			atomic.AddInt32(f.requests["statuses"], 1)
			page := 0
			fmt.Sscan(r.URL.Query().Get("page"), &page)
			body := f.statuses[page]
			next := ""
			if page+1 < len(f.statuses) {
				next = fmt.Sprintf(`, "next": "%s/repositories/acme/api/commit/abc123def456/statuses?pagelen=100&page=%d"`, f.URL, page+1)
			}
			fmt.Fprintf(w, `{"values": [%s]%s}`, body, next)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeBitbucket) client(email, token string) *Client {
	c := NewClient(email, token)
	c.SetBaseURL(f.URL)
	return c
}

func status(key, name, state, created, updated string) string {
	return fmt.Sprintf(`{"key": %q, "name": %q, "state": %q, "url": "https://bitbucket.org/acme/api/pipelines/results/%s",
		"created_on": "2026-10-24T%s+00:00", "updated_on": "2026-10-24T%s+00:00"}`, key, name, state, key, created, updated)
}

func TestGetPullRequest_States(t *testing.T) {
	f := newFake(t)
	c := f.client("", "")
	for state, want := range map[string]struct {
		state  string
		merged bool
	}{
		"OPEN": {"open", false}, "MERGED": {"closed", true}, "DECLINED": {"closed", false}, "SUPERSEDED": {"closed", false},
	} {
		f.prState = state
		pr, err := c.GetPullRequest(context.Background(), "acme", "api", 42)
		if err != nil {
			t.Fatal(err)
		}
		if pr.State != want.state || pr.Merged != want.merged || pr.Head.SHA != "abc123def456" || pr.Title != "Add login" || pr.Number != 42 {
			t.Errorf("%s → %+v", state, pr)
		}
		if pr.UpdatedAt.IsZero() {
			t.Errorf("%s: updated_on not parsed", state)
		}
	}
}

func TestGetPullRequest_MergeState(t *testing.T) {
	tests := []struct {
		name, checks, want string
	}{
		{"clean", `{"type":"git_mergeability_check","status":"PASSED","blocking":false,"reason":"clean"},
			{"type":"current_user_permission_check","status":"FAILED","blocking":true}`, "clean"},
		{"conflict", `{"type":"git_mergeability_check","status":"FAILED","blocking":true,"reason":"conflict"},
			{"type":"standard_merge_check","status":"FAILED","blocking":true}`, "dirty"},
		{"needs approval", `{"type":"git_mergeability_check","status":"PASSED","blocking":false},
			{"type":"standard_merge_check","status":"FAILED","blocking":true}`, "blocked"},
		{"non-blocking failure", `{"type":"standard_merge_check","status":"FAILED","blocking":false}`, "clean"},
		{"unavailable", "", ""},
	}
	for _, tt := range tests {
		f := newFake(t)
		f.mergeChecks = tt.checks
		pr, err := f.client("", "").GetPullRequest(context.Background(), "acme", "api", 42)
		if err != nil {
			t.Fatal(err)
		}
		if pr.MergeableState != tt.want {
			t.Errorf("%s: mergeable_state = %q, want %q", tt.name, pr.MergeableState, tt.want)
		}
	}
}

func TestGetPullRequest_ReusesMergeChecksWhileThePRIsUnchanged(t *testing.T) {
	f := newFake(t)
	f.mergeChecks = `{"type":"git_mergeability_check","status":"PASSED"}`
	c := f.client("", "")
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		c.GetPullRequest(context.Background(), "acme", "api", 42)
	}
	if n := atomic.LoadInt32(f.requests["merge"]); n != 1 {
		t.Errorf("merge checks read %d times in a row, want 1 (Bitbucket allows 1,000 requests/hour)", n)
	}
	now = now.Add(mergeabilityTTL + time.Second)
	c.GetPullRequest(context.Background(), "acme", "api", 42)
	if n := atomic.LoadInt32(f.requests["merge"]); n != 2 {
		t.Errorf("merge checks must be re-read after %s (the base branch may have moved): %d reads", mergeabilityTTL, n)
	}
	f.prState = "MERGED"
	c.GetPullRequest(context.Background(), "acme", "api", 42)
	if n := atomic.LoadInt32(f.requests["merge"]); n != 2 {
		t.Errorf("closed PRs have no merge checks to read: %d reads", n)
	}
}

func TestGetCheckRuns(t *testing.T) {
	f := newFake(t)
	f.statuses = []string{
		status("1", "Pipeline - default", "FAILED", "10:00:00", "10:08:30") + "," + status("2", "", "INPROGRESS", "10:05:00", "10:05:00"),
		status("3", "Jenkins build", "SUCCESSFUL", "09:00:00", "09:01:00") + "," + status("4", "Pipeline - deploy", "STOPPED", "10:00:00", "10:02:00"),
	}
	c := f.client("", "")
	runs, err := c.GetCheckRuns(context.Background(), "acme", "api", "abc123def456")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range runs {
		got = append(got, fmt.Sprintf("%s=%s/%s", r.Name, r.Status, r.Conclusion))
	}
	want := "Pipeline - default=completed/failure 2=in_progress/ Jenkins build=completed/success Pipeline - deploy=completed/cancelled"
	if strings.Join(got, " ") != want {
		t.Errorf("runs = %s\nwant   %s", strings.Join(got, " "), want)
	}
	if d := runs[0].CompletedAt.Sub(runs[0].StartedAt); d != 8*time.Minute+30*time.Second {
		t.Errorf("duration = %s", d)
	}
	if !runs[1].CompletedAt.IsZero() || runs[0].HTMLURL != "https://bitbucket.org/acme/api/pipelines/results/1" {
		t.Errorf("runs = %+v", runs[:2])
	}

	status, _ := c.GetCombinedStatus(context.Background(), "acme", "api", "abc123def456")
	ci := github.SummarizeCI(status, runs)
	if ci.State != "failure" || ci.Total != 4 || ci.Pending != 1 || len(ci.FailedChecks) != 2 {
		t.Errorf("summary = %+v, want failure with the failed and the stopped build", ci)
	}
}

func TestGetCheckRuns_NoBuilds(t *testing.T) {
	f := newFake(t)
	f.statuses = []string{""}
	c := f.client("", "")
	runs, _ := c.GetCheckRuns(context.Background(), "acme", "api", "abc123def456")
	status, _ := c.GetCombinedStatus(context.Background(), "acme", "api", "abc123def456")
	if ci := github.SummarizeCI(status, runs); ci.State != "pending" || ci.Total != 0 {
		t.Errorf("summary = %+v; no builds must look like no CI (pending with 0 checks)", ci)
	}
}

func TestGetReviews(t *testing.T) {
	f := newFake(t)
	c := f.client("", "")
	c.GetPullRequest(context.Background(), "acme", "api", 42)
	reviews, err := c.GetReviews(context.Background(), "acme", "api", 42)
	if err != nil {
		t.Fatal(err)
	}
	if len(reviews) != 2 || reviews[0].State != "APPROVED" || reviews[1].State != "CHANGES_REQUESTED" {
		t.Errorf("reviews = %+v", reviews)
	}
	if github.ReviewDecision(reviews) != github.ReviewChangesRequested {
		t.Error("a change request must win over an approval")
	}
	if n := atomic.LoadInt32(f.requests["pr"]); n != 1 {
		t.Errorf("GetReviews must reuse the PR just read: %d PR requests", n)
	}
}

func TestAuthentication(t *testing.T) {
	f := newFake(t)
	f.client("dev@example.com", "api-token").GetPullRequest(context.Background(), "acme", "api", 42)
	if want := "Basic " + base64.StdEncoding.EncodeToString([]byte("dev@example.com:api-token")); f.lastAuth != want {
		t.Errorf("API token: Authorization = %q", f.lastAuth)
	}
	f.client("", "access-token").GetPullRequest(context.Background(), "acme", "api", 42)
	if f.lastAuth != "Bearer access-token" {
		t.Errorf("access token: Authorization = %q", f.lastAuth)
	}
	f.client("", "").GetPullRequest(context.Background(), "acme", "api", 42)
	if f.lastAuth != "" {
		t.Errorf("anonymous: Authorization = %q", f.lastAuth)
	}
}

func TestErrorsNameBitbucket(t *testing.T) {
	f := newFake(t)
	_, err := f.client("", "").GetPullRequest(context.Background(), "acme", "missing", 1)
	if err == nil || !strings.Contains(err.Error(), "Bitbucket API returned 404") {
		t.Errorf("err = %v", err)
	}
}

func TestRelativeDoesNotFollowOtherHosts(t *testing.T) {
	c := NewClient("", "token")
	if got := c.relative("https://evil.example.com/steal?page=2"); got != "" {
		t.Errorf("relative = %q; credentials must not be sent to another host", got)
	}
	if got := c.relative(DefaultBaseURL + "/repositories/a/b/commit/c/statuses?page=2"); got != "/repositories/a/b/commit/c/statuses?page=2" {
		t.Errorf("relative = %q", got)
	}
}
