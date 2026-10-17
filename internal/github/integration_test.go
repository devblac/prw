//go:build integration

// Integration tests against the real GitHub API. They are excluded from the normal
// `go test ./...` run and executed nightly by .github/workflows/integration.yml, or
// locally with `make integration`.
//
// They exist because unit tests with mocked responses can't catch a wrong assumption
// about the API itself: the original prw only read /commits/{sha}/status, which never
// reports GitHub Actions results, and every mocked test still passed.
//
// The fixtures are immutable objects in this repository (a commit SHA and a merged
// PR), so the expected results never change. GITHUB_TOKEN is used when set (CI provides
// one); otherwise the requests are unauthenticated, which works for public repos.
package github

import (
	"errors"
	"net/http"
	"os"
	"testing"
)

const (
	fixtureOwner = "devblac"
	fixtureRepo  = "prw"

	// A commit whose CI ran only on GitHub Actions: /status reports "pending" with zero
	// statuses, while /check-runs has two successful runs (Test, Build).
	fixtureActionsOnlySHA = "a6f00b9f5ece056cafa1757771eb79275fd75f91"

	// A merged pull request.
	fixtureMergedPR = 5
)

func integrationClient(t *testing.T) *Client {
	t.Helper()
	return NewClient(os.Getenv("GITHUB_TOKEN"))
}

func TestIntegration_ActionsOnlyCommitResolvesToSuccess(t *testing.T) {
	c := integrationClient(t)

	status, err := c.GetCombinedStatus(fixtureOwner, fixtureRepo, fixtureActionsOnlySHA)
	if err != nil {
		t.Fatalf("GetCombinedStatus: %v", err)
	}
	// Documents the API behavior prw has to work around.
	if NormalizeState(status.State) != "pending" || status.TotalCount != 0 {
		t.Logf("note: legacy status changed to state=%q total_count=%d", status.State, status.TotalCount)
	}

	runs, err := c.GetCheckRuns(fixtureOwner, fixtureRepo, fixtureActionsOnlySHA)
	if err != nil {
		t.Fatalf("GetCheckRuns: %v", err)
	}
	if len(runs) == 0 {
		t.Fatal("expected check runs for an Actions-only commit, got none")
	}

	ci := SummarizeCI(status, runs)
	if ci.State != "success" {
		t.Errorf("aggregated state = %q, want success (legacy=%q/%d, %d check runs, failed=%v)",
			ci.State, status.State, status.TotalCount, len(runs), ci.FailedChecks)
	}
}

func TestIntegration_FetchCIStatus(t *testing.T) {
	ci, err := FetchCIStatus(integrationClient(t), fixtureOwner, fixtureRepo, fixtureActionsOnlySHA)
	if err != nil {
		t.Fatalf("FetchCIStatus: %v", err)
	}
	if ci.State != "success" {
		t.Errorf("state = %q, want success", ci.State)
	}
	if len(ci.FailedChecks) != 0 {
		t.Errorf("expected no failed checks, got %v", ci.FailedChecks)
	}
}

func TestIntegration_MergedPullRequest(t *testing.T) {
	pr, err := integrationClient(t).GetPullRequest(fixtureOwner, fixtureRepo, fixtureMergedPR)
	if err != nil {
		t.Fatalf("GetPullRequest: %v", err)
	}
	if pr.Number != fixtureMergedPR {
		t.Errorf("number = %d, want %d", pr.Number, fixtureMergedPR)
	}
	if !pr.IsClosed() || !pr.Merged {
		t.Errorf("expected PR #%d to be closed and merged, got state=%q merged=%v", fixtureMergedPR, pr.State, pr.Merged)
	}
	if pr.Title == "" || pr.Head.SHA == "" {
		t.Errorf("expected title and head SHA to be populated, got %+v", pr)
	}
}

// statusRecorder records the HTTP status of every response.
type statusRecorder struct {
	codes []int
}

func (s *statusRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err == nil {
		s.codes = append(s.codes, resp.StatusCode)
	}
	return resp, err
}

func TestIntegration_ConditionalRequestsAreFree(t *testing.T) {
	if os.Getenv("GITHUB_TOKEN") == "" {
		// Unauthenticated 304s still count against the 60/h limit; GitHub only
		// exempts them for authenticated requests.
		t.Skip("needs GITHUB_TOKEN")
	}
	c := integrationClient(t)
	rec := &statusRecorder{}
	c.HTTPClient.Transport = rec

	first, err := c.GetPullRequest(fixtureOwner, fixtureRepo, fixtureMergedPR)
	if err != nil {
		t.Fatalf("first request: %v", err)
	}
	before := c.RateLimit()

	second, err := c.GetPullRequest(fixtureOwner, fixtureRepo, fixtureMergedPR)
	if err != nil {
		t.Fatalf("second request: %v", err)
	}
	after := c.RateLimit()

	if len(rec.codes) != 2 || rec.codes[0] != http.StatusOK || rec.codes[1] != http.StatusNotModified {
		t.Fatalf("status codes = %v, want [200 304]", rec.codes)
	}
	if second.Title != first.Title || second.Head.SHA != first.Head.SHA {
		t.Errorf("cached body differs: %+v vs %+v", second, first)
	}
	if !before.Known || !after.Known {
		t.Fatalf("rate limit headers missing: %+v / %+v", before, after)
	}
	// Other clients sharing the token may spend quota concurrently, so only require
	// that this 304 didn't cost anything when the window didn't roll over.
	if after.Reset.Equal(before.Reset) && after.Remaining < before.Remaining {
		t.Errorf("304 consumed quota: remaining %d -> %d", before.Remaining, after.Remaining)
	}
	t.Logf("rate limit: %d/%d remaining (unchanged by the 304)", after.Remaining, after.Limit)
}

func TestIntegration_MissingPullRequestIs404(t *testing.T) {
	_, err := integrationClient(t).GetPullRequest(fixtureOwner, fixtureRepo, 999999)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("expected a 404 APIError, got %v", err)
	}
}
