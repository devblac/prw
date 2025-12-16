package github

import (
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

			pr, err := client.GetPullRequest("owner", "repo", 123)
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

			status, err := client.GetCombinedStatus("owner", "repo", "abc123")
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

	_, err := client.GetPullRequest("owner", "repo", 123)
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

	_, err := client.GetCombinedStatus("owner", "repo", "abc123")
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

	_, err := client.GetPullRequest("owner", "repo", 123)
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

	_, err := client.GetCombinedStatus("owner", "repo", "abc123")
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

	_, err := client.GetPullRequest("owner", "repo", 123)
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

	_, err := client.GetCombinedStatus("owner", "repo", "abc123")
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

	status, err := client.GetCombinedStatus("owner", "repo", "abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.State != "" {
		t.Errorf("expected empty state, got %q", status.State)
	}
}
