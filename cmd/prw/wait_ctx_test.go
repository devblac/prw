package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/devblac/prw/internal/github"
)

func TestWaitForCI_FollowsPushThatLandsDuringThePoll(t *testing.T) {
	// The PR is read at "old", then "new" is pushed before the CI lookup of "old"
	// returns success: that result must not be reported for the PR.
	noSleep(t)
	client := &scriptedClient{steps: []pollStep{
		{pr: openPR("old"), runs: []github.CheckRun{run("Test", "completed", "success")}, movedTo: openPR("new")},
		{pr: openPR("new"), runs: []github.CheckRun{run("Test", "in_progress", "")}},
		{pr: openPR("new"), runs: []github.CheckRun{run("Test", "completed", "failure")}},
	}}

	result, err := waitForCI(context.Background(), client, "o", "r", 1, time.Second, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if result.SHA != "new" || result.State != "failure" {
		t.Errorf("got %s on %s, want the failure of the new head", result.State, result.SHA)
	}
}

func TestWaitForCI_ConfirmsHeadBeforeReturning(t *testing.T) {
	noSleep(t)
	client := &scriptedClient{steps: []pollStep{{pr: openPR("sha"), runs: []github.CheckRun{run("Test", "completed", "success")}}}}

	result, err := waitForCI(context.Background(), client, "o", "r", 1, time.Second, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "success" || client.polls != 1 || client.revalidations != 1 {
		t.Errorf("state=%q polls=%d revalidations=%d, want success after one poll and one head check",
			result.State, client.polls, client.revalidations)
	}
}

// hangingClient is a real client against a server that never answers until the
// request is cancelled, like a stalled network.
func hangingClient(t *testing.T) *github.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	}))
	t.Cleanup(srv.Close)
	client := github.NewClient("token")
	client.BaseURL = srv.URL
	return client
}

func TestWaitForCI_TimeoutCancelsTheRequestInFlight(t *testing.T) {
	noSleep(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	result, err := waitForCI(ctx, hangingClient(t), "o", "r", 1, time.Second, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("returned after %s; the timeout must cancel the hanging request", elapsed)
	}
	if !result.TimedOut || exitCode(result.exitErr()) != exitWaitTimeout {
		t.Errorf("result = %+v, want timed out (exit 2)", result)
	}
}

func TestWaitForCI_InterruptCancelsTheRequestInFlight(t *testing.T) {
	noSleep(t)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel) // Ctrl-C

	start := time.Now()
	result, err := waitForCI(ctx, hangingClient(t), "o", "r", 1, time.Second, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("returned after %s; Ctrl-C must cancel the hanging request", elapsed)
	}
	if !result.Interrupted || exitCode(result.exitErr()) != exitWaitInterrupted {
		t.Errorf("result = %+v, want interrupted (exit 130)", result)
	}
}
