package github

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// mockRoundTripper implements http.RoundTripper for testing.
type mockRoundTripper struct {
	response *http.Response
	err      error
}

func (m *mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.response, nil
}

func TestParsePRURL(t *testing.T) {
	tests := []struct {
		name      string
		url       string
		wantOwner string
		wantRepo  string
		wantNum   int
		wantErr   bool
	}{
		{
			name:      "full HTTPS URL",
			url:       "https://github.com/owner/repo/pull/123",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantNum:   123,
		},
		{
			name:      "HTTP URL",
			url:       "http://github.com/owner/repo/pull/456",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantNum:   456,
		},
		{
			name:      "without protocol",
			url:       "github.com/owner/repo/pull/789",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantNum:   789,
		},
		{
			name:    "invalid URL",
			url:     "not a github url",
			wantErr: true,
		},
		{
			name:    "missing PR number",
			url:     "https://github.com/owner/repo/pull/",
			wantErr: true,
		},
		{
			name:    "not a pull request",
			url:     "https://github.com/owner/repo/issues/123",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner, repo, num, err := ParsePRURL(tt.url)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if owner != tt.wantOwner || repo != tt.wantRepo || num != tt.wantNum {
				t.Errorf("got (%s, %s, %d), want (%s, %s, %d)",
					owner, repo, num, tt.wantOwner, tt.wantRepo, tt.wantNum)
			}
		})
	}
}

func TestGetPullRequest(t *testing.T) {
	tests := []struct {
		name         string
		responseBody string
		statusCode   int
		wantErr      bool
		checkAuth    bool
	}{
		{
			name: "successful request",
			responseBody: `{
				"number": 123,
				"title": "Test PR",
				"head": {"sha": "abc123def456"}
			}`,
			statusCode: http.StatusOK,
		},
		{
			name:         "not found",
			responseBody: `{"message": "Not Found"}`,
			statusCode:   http.StatusNotFound,
			wantErr:      true,
		},
		{
			name: "check authorization header",
			responseBody: `{
				"number": 1,
				"title": "Test",
				"head": {"sha": "sha"}
			}`,
			statusCode: http.StatusOK,
			checkAuth:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedReq *http.Request
			client := &Client{
				BaseURL: "https://api.github.com",
				Token:   "test-token",
				HTTPClient: &http.Client{
					Transport: &mockRoundTripperFunc{
						fn: func(req *http.Request) (*http.Response, error) {
							capturedReq = req
							return &http.Response{
								StatusCode: tt.statusCode,
								Body:       io.NopCloser(strings.NewReader(tt.responseBody)),
								Header:     make(http.Header),
							}, nil
						},
					},
				},
			}

			pr, err := client.GetPullRequest(context.Background(), "owner", "repo", 123)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.checkAuth {
				auth := capturedReq.Header.Get("Authorization")
				if auth != "Bearer test-token" {
					t.Errorf("expected Authorization header 'Bearer test-token', got %q", auth)
				}
				accept := capturedReq.Header.Get("Accept")
				if accept != "application/vnd.github.v3+json" {
					t.Errorf("expected Accept header for GitHub API v3, got %q", accept)
				}
			}

			if pr == nil {
				t.Fatal("expected PR, got nil")
			}
			if pr.Number != 123 && pr.Number != 1 {
				t.Errorf("unexpected PR number: %d", pr.Number)
			}
		})
	}
}

func TestGetCombinedStatus(t *testing.T) {
	tests := []struct {
		name         string
		responseBody string
		statusCode   int
		wantState    string
		wantErr      bool
	}{
		{
			name: "success state",
			responseBody: `{
				"state": "success",
				"sha": "abc123"
			}`,
			statusCode: http.StatusOK,
			wantState:  "success",
		},
		{
			name: "pending state",
			responseBody: `{
				"state": "pending",
				"sha": "def456"
			}`,
			statusCode: http.StatusOK,
			wantState:  "pending",
		},
		{
			name:         "API error",
			responseBody: `{"message": "Bad credentials"}`,
			statusCode:   http.StatusUnauthorized,
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &Client{
				BaseURL: "https://api.github.com",
				Token:   "test-token",
				HTTPClient: &http.Client{
					Transport: &mockRoundTripperFunc{
						fn: func(req *http.Request) (*http.Response, error) {
							return &http.Response{
								StatusCode: tt.statusCode,
								Body:       io.NopCloser(strings.NewReader(tt.responseBody)),
								Header:     make(http.Header),
							}, nil
						},
					},
				},
			}

			status, err := client.GetCombinedStatus(context.Background(), "owner", "repo", "abc123")
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if status.State != tt.wantState {
				t.Errorf("expected state %q, got %q", tt.wantState, status.State)
			}
		})
	}
}

func TestFormatPRURL(t *testing.T) {
	url := FormatPRURL("owner", "repo", 123)
	expected := "https://github.com/owner/repo/pull/123"
	if url != expected {
		t.Errorf("expected %q, got %q", expected, url)
	}
}

func TestNormalizeState(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"SUCCESS", "success"},
		{"Pending", "pending"},
		{"failure", "failure"},
		{"  error  ", "error"},
	}

	for _, tt := range tests {
		result := NormalizeState(tt.input)
		if result != tt.expected {
			t.Errorf("NormalizeState(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

// mockRoundTripperFunc is a helper to create mock round trippers with a function.
type mockRoundTripperFunc struct {
	fn func(*http.Request) (*http.Response, error)
}

func (m *mockRoundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.fn(req)
}

func TestNewClient(t *testing.T) {
	client := NewClient("test-token")

	if client.BaseURL != "https://api.github.com" {
		t.Errorf("expected BaseURL 'https://api.github.com', got %q", client.BaseURL)
	}

	if client.Token != "test-token" {
		t.Errorf("expected Token 'test-token', got %q", client.Token)
	}

	if client.HTTPClient == nil {
		t.Fatal("expected HTTPClient to be initialized")
	}

	expectedTimeout := 15 * time.Second
	if client.HTTPClient.Timeout != expectedTimeout {
		t.Errorf("expected HTTP client timeout %v, got %v", expectedTimeout, client.HTTPClient.Timeout)
	}
}

func TestNewClientWithTimeout(t *testing.T) {
	client := NewClient("test-token")

	// Verify the client has a timeout set to prevent hanging requests
	if client.HTTPClient.Timeout == 0 {
		t.Error("HTTP client should have a timeout set to prevent indefinite hangs")
	}

	if client.HTTPClient.Timeout < 5*time.Second {
		t.Errorf("timeout %v seems too short for API requests", client.HTTPClient.Timeout)
	}

	if client.HTTPClient.Timeout > 30*time.Second {
		t.Errorf("timeout %v seems unnecessarily long", client.HTTPClient.Timeout)
	}
}

func TestGetPullRequest_NetworkError(t *testing.T) {
	client := &Client{
		BaseURL: "https://api.github.com",
		Token:   "test-token",
		HTTPClient: &http.Client{
			Transport: &mockRoundTripperFunc{
				fn: func(req *http.Request) (*http.Response, error) {
					return nil, fmt.Errorf("network error")
				},
			},
		},
	}

	_, err := client.GetPullRequest(context.Background(), "owner", "repo", 123)
	if err == nil {
		t.Error("expected error for network failure, got nil")
	}
	if !strings.Contains(err.Error(), "request failed") {
		t.Errorf("error should mention request failed: %v", err)
	}
}

func TestGetCombinedStatus_NetworkError(t *testing.T) {
	client := &Client{
		BaseURL: "https://api.github.com",
		Token:   "test-token",
		HTTPClient: &http.Client{
			Transport: &mockRoundTripperFunc{
				fn: func(req *http.Request) (*http.Response, error) {
					return nil, fmt.Errorf("network error")
				},
			},
		},
	}

	_, err := client.GetCombinedStatus(context.Background(), "owner", "repo", "abc123")
	if err == nil {
		t.Error("expected error for network failure, got nil")
	}
	if !strings.Contains(err.Error(), "request failed") {
		t.Errorf("error should mention request failed: %v", err)
	}
}

func TestGetPullRequest_InvalidJSON(t *testing.T) {
	client := &Client{
		BaseURL: "https://api.github.com",
		Token:   "test-token",
		HTTPClient: &http.Client{
			Transport: &mockRoundTripperFunc{
				fn: func(req *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader("invalid json {")),
						Header:     make(http.Header),
					}, nil
				},
			},
		},
	}

	_, err := client.GetPullRequest(context.Background(), "owner", "repo", 123)
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
	if !strings.Contains(err.Error(), "failed to decode") {
		t.Errorf("error should mention decode failure: %v", err)
	}
}

func TestGetCombinedStatus_InvalidJSON(t *testing.T) {
	client := &Client{
		BaseURL: "https://api.github.com",
		Token:   "test-token",
		HTTPClient: &http.Client{
			Transport: &mockRoundTripperFunc{
				fn: func(req *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader("invalid json {")),
						Header:     make(http.Header),
					}, nil
				},
			},
		},
	}

	_, err := client.GetCombinedStatus(context.Background(), "owner", "repo", "abc123")
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
	if !strings.Contains(err.Error(), "failed to decode") {
		t.Errorf("error should mention decode failure: %v", err)
	}
}

func TestGetPullRequest_ErrorResponse(t *testing.T) {
	client := &Client{
		BaseURL: "https://api.github.com",
		Token:   "test-token",
		HTTPClient: &http.Client{
			Transport: &mockRoundTripperFunc{
				fn: func(req *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusForbidden,
						Body:       io.NopCloser(strings.NewReader(`{"message": "Forbidden"}`)),
						Header:     make(http.Header),
					}, nil
				},
			},
		},
	}

	_, err := client.GetPullRequest(context.Background(), "owner", "repo", 123)
	if err == nil {
		t.Error("expected error for 403 response, got nil")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error should mention status code: %v", err)
	}
}

func TestGetCombinedStatus_ErrorResponse(t *testing.T) {
	client := &Client{
		BaseURL: "https://api.github.com",
		Token:   "test-token",
		HTTPClient: &http.Client{
			Transport: &mockRoundTripperFunc{
				fn: func(req *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusNotFound,
						Body:       io.NopCloser(strings.NewReader(`{"message": "Not Found"}`)),
						Header:     make(http.Header),
					}, nil
				},
			},
		},
	}

	_, err := client.GetCombinedStatus(context.Background(), "owner", "repo", "abc123")
	if err == nil {
		t.Error("expected error for 404 response, got nil")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error should mention status code: %v", err)
	}
}

func TestParsePRURL_EdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{
			name:    "URL with query params",
			url:     "https://github.com/owner/repo/pull/123?tab=files",
			wantErr: false,
		},
		{
			name:    "URL with fragment",
			url:     "https://github.com/owner/repo/pull/123#discussion",
			wantErr: false,
		},
		{
			name:    "URL with trailing slash",
			url:     "https://github.com/owner/repo/pull/123/",
			wantErr: false,
		},
		{
			name:    "invalid number",
			url:     "https://github.com/owner/repo/pull/abc",
			wantErr: true,
		},
		{
			name:    "empty owner",
			url:     "https://github.com//repo/pull/123",
			wantErr: true,
		},
		{
			name:    "empty repo",
			url:     "https://github.com/owner//pull/123",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, _, err := ParsePRURL(tt.url)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestGetCombinedStatus_EmptyState(t *testing.T) {
	client := &Client{
		BaseURL: "https://api.github.com",
		Token:   "test-token",
		HTTPClient: &http.Client{
			Transport: &mockRoundTripperFunc{
				fn: func(req *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader(`{"state": "", "sha": "abc123"}`)),
						Header:     make(http.Header),
					}, nil
				},
			},
		},
	}

	status, err := client.GetCombinedStatus(context.Background(), "owner", "repo", "abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.State != "" {
		t.Errorf("expected empty state, got %q", status.State)
	}
}

func jsonResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func TestGetJSON_OmitsAuthorizationWithoutToken(t *testing.T) {
	var gotAuth []string
	client := &Client{
		BaseURL: "https://api.github.com",
		HTTPClient: &http.Client{
			Transport: &mockRoundTripperFunc{
				fn: func(req *http.Request) (*http.Response, error) {
					gotAuth = req.Header.Values("Authorization")
					return jsonResponse(http.StatusOK, `{"number":1}`), nil
				},
			},
		},
	}

	if _, err := client.GetPullRequest(context.Background(), "owner", "repo", 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(gotAuth) != 0 {
		t.Errorf("expected no Authorization header without a token, got %q", gotAuth)
	}
}

func TestGetCheckRuns_Paginates(t *testing.T) {
	var requestedPages []string
	client := &Client{
		BaseURL: "https://api.github.com",
		Token:   "test-token",
		HTTPClient: &http.Client{
			Transport: &mockRoundTripperFunc{
				fn: func(req *http.Request) (*http.Response, error) {
					if !strings.HasSuffix(req.URL.Path, "/repos/owner/repo/commits/abc123/check-runs") {
						t.Errorf("unexpected path: %s", req.URL.Path)
					}
					page := req.URL.Query().Get("page")
					requestedPages = append(requestedPages, page)

					count := checkRunsPerPage
					if page == "2" {
						count = 1
					}
					runs := make([]string, 0, count)
					for i := 0; i < count; i++ {
						runs = append(runs, fmt.Sprintf(`{"name":"job-%s-%d","status":"completed","conclusion":"success"}`, page, i))
					}
					body := fmt.Sprintf(`{"total_count":%d,"check_runs":[%s]}`, checkRunsPerPage+1, strings.Join(runs, ","))
					return jsonResponse(http.StatusOK, body), nil
				},
			},
		},
	}

	runs, err := client.GetCheckRuns(context.Background(), "owner", "repo", "abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(runs) != checkRunsPerPage+1 {
		t.Errorf("expected %d runs, got %d", checkRunsPerPage+1, len(runs))
	}
	if len(requestedPages) != 2 {
		t.Errorf("expected 2 page requests, got %v", requestedPages)
	}
}

func TestGetCheckRuns_ForbiddenHint(t *testing.T) {
	client := &Client{
		BaseURL: "https://api.github.com",
		Token:   "test-token",
		HTTPClient: &http.Client{
			Transport: &mockRoundTripperFunc{
				fn: func(req *http.Request) (*http.Response, error) {
					return jsonResponse(http.StatusForbidden, `{"message":"Resource not accessible by personal access token"}`), nil
				},
			},
		},
	}

	_, err := client.GetCheckRuns(context.Background(), "owner", "repo", "abc123")
	if err == nil {
		t.Fatal("expected error for 403 response, got nil")
	}
	if !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "Checks: read") {
		t.Errorf("error should mention status code and required permission: %v", err)
	}
}

func TestFetchCIStatus_ActionsOnlyRepo(t *testing.T) {
	// Mirrors the real API for a repo that only uses GitHub Actions: the legacy
	// status endpoint reports pending with zero statuses.
	client := &Client{
		BaseURL: "https://api.github.com",
		Token:   "test-token",
		HTTPClient: &http.Client{
			Transport: &mockRoundTripperFunc{
				fn: func(req *http.Request) (*http.Response, error) {
					if strings.HasSuffix(req.URL.Path, "/status") {
						return jsonResponse(http.StatusOK, `{"state":"pending","sha":"abc123","total_count":0,"statuses":[]}`), nil
					}
					return jsonResponse(http.StatusOK, `{"total_count":2,"check_runs":[
						{"name":"Test","status":"completed","conclusion":"failure","html_url":"https://github.com/owner/repo/runs/1"},
						{"name":"Build","status":"completed","conclusion":"success"}
					]}`), nil
				},
			},
		},
	}

	ci, err := FetchCIStatus(context.Background(), client, "owner", "repo", "abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ci.State != "failure" {
		t.Errorf("expected failure, got %q", ci.State)
	}
	if len(ci.FailedChecks) != 1 || ci.FailedChecks[0].Name != "Test" || ci.FailedChecks[0].URL == "" {
		t.Errorf("unexpected failed checks: %+v", ci.FailedChecks)
	}
}

func TestFetchCIStatus_PropagatesErrors(t *testing.T) {
	client := &Client{
		BaseURL: "https://api.github.com",
		Token:   "test-token",
		HTTPClient: &http.Client{
			Transport: &mockRoundTripperFunc{
				fn: func(req *http.Request) (*http.Response, error) {
					if strings.HasSuffix(req.URL.Path, "/status") {
						return jsonResponse(http.StatusOK, `{"state":"success","total_count":1}`), nil
					}
					return jsonResponse(http.StatusInternalServerError, `{"message":"boom"}`), nil
				},
			},
		},
	}

	_, err := FetchCIStatus(context.Background(), client, "owner", "repo", "abc123")
	if err == nil || !strings.Contains(err.Error(), "check runs") {
		t.Errorf("expected check runs error, got %v", err)
	}
}

func TestSummarizeCI_Counts(t *testing.T) {
	status := &CombinedStatus{State: "pending", TotalCount: 2, Statuses: []CommitStatus{
		{Context: "ci/jenkins", State: "pending"},
		{Context: "ci/lint", State: "success"},
	}}
	runs := []CheckRun{
		{Name: "Test", Status: "in_progress"},
		{Name: "Build", Status: "queued"},
		{Name: "Docs", Status: "completed", Conclusion: "success"},
	}
	ci := SummarizeCI(status, runs)
	if ci.Total != 5 || ci.Pending != 3 {
		t.Errorf("Total/Pending = %d/%d, want 5/3", ci.Total, ci.Pending)
	}
	if empty := SummarizeCI(&CombinedStatus{State: "pending"}, nil); empty.Total != 0 || empty.Pending != 0 {
		t.Errorf("no CI should count 0/0, got %d/%d", empty.Total, empty.Pending)
	}
}

func TestSummarizeCI(t *testing.T) {
	noStatuses := &CombinedStatus{State: "pending", TotalCount: 0}
	completed := func(name, conclusion string) CheckRun {
		return CheckRun{Name: name, Status: "completed", Conclusion: conclusion}
	}

	tests := []struct {
		name       string
		status     *CombinedStatus
		runs       []CheckRun
		wantState  string
		wantFailed []string
	}{
		{name: "no CI at all", status: noStatuses, wantState: "pending"},
		{name: "nil status and no runs", wantState: "pending"},
		{
			name:      "actions only, all green",
			status:    noStatuses,
			runs:      []CheckRun{completed("Test", "success"), completed("Build", "success")},
			wantState: "success",
		},
		{
			name:       "actions only, one failed",
			status:     noStatuses,
			runs:       []CheckRun{completed("Lint", "failure"), completed("Build", "success")},
			wantState:  "failure",
			wantFailed: []string{"Lint"},
		},
		{
			name:      "actions still running",
			status:    noStatuses,
			runs:      []CheckRun{{Name: "Test", Status: "in_progress"}, completed("Build", "success")},
			wantState: "pending",
		},
		{
			name:       "failure wins over pending",
			status:     noStatuses,
			runs:       []CheckRun{{Name: "Test", Status: "queued"}, completed("Build", "timed_out")},
			wantState:  "failure",
			wantFailed: []string{"Build"},
		},
		{
			name:       "cancelled counts as failure",
			status:     noStatuses,
			runs:       []CheckRun{completed("Deploy", "cancelled")},
			wantState:  "failure",
			wantFailed: []string{"Deploy"},
		},
		{
			name:      "neutral and skipped count as passing",
			status:    noStatuses,
			runs:      []CheckRun{completed("Docs", "skipped"), completed("Optional", "neutral")},
			wantState: "success",
		},
		{
			name:      "legacy statuses only",
			status:    &CombinedStatus{State: "success", TotalCount: 1},
			wantState: "success",
		},
		{
			name:      "legacy state without total_count",
			status:    &CombinedStatus{State: "success"},
			wantState: "success",
		},
		{
			name: "legacy error with green check runs",
			status: &CombinedStatus{State: "error", TotalCount: 1, Statuses: []CommitStatus{
				{Context: "ci/jenkins", State: "error", TargetURL: "https://jenkins.example/1"},
			}},
			runs:       []CheckRun{completed("Build", "success")},
			wantState:  "error",
			wantFailed: []string{"ci/jenkins"},
		},
		{
			name:       "legacy success with failed check run",
			status:     &CombinedStatus{State: "success", TotalCount: 1},
			runs:       []CheckRun{completed("Test", "failure")},
			wantState:  "failure",
			wantFailed: []string{"Test"},
		},
		{
			name:      "legacy pending with statuses stays pending",
			status:    &CombinedStatus{State: "pending", TotalCount: 1},
			runs:      []CheckRun{completed("Build", "success")},
			wantState: "pending",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ci := SummarizeCI(tt.status, tt.runs)
			if ci.State != tt.wantState {
				t.Errorf("state = %q, want %q", ci.State, tt.wantState)
			}
			var gotFailed []string
			for _, fc := range ci.FailedChecks {
				gotFailed = append(gotFailed, fc.Name)
			}
			if strings.Join(gotFailed, ",") != strings.Join(tt.wantFailed, ",") {
				t.Errorf("failed checks = %v, want %v", gotFailed, tt.wantFailed)
			}
		})
	}
}
