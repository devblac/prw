package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	c := NewClient("test-token")
	c.BaseURL = server.URL
	c.HTTPClient = server.Client()
	return c
}

func TestGetJSON_UsesETagAndServesCachedBodyOn304(t *testing.T) {
	var requests, conditional int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		if r.Header.Get("If-None-Match") == `"v1"` {
			atomic.AddInt32(&conditional, 1)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		fmt.Fprint(w, `{"number":7,"title":"Cached","state":"open","head":{"sha":"abc"}}`)
	})

	for i := 0; i < 3; i++ {
		pr, err := c.GetPullRequest(context.Background(), "o", "r", 7)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		if pr.Title != "Cached" || pr.Head.SHA != "abc" {
			t.Fatalf("request %d decoded %+v", i, pr)
		}
	}
	if requests != 3 || conditional != 2 {
		t.Errorf("requests=%d conditional=%d, want 3 and 2 (first unconditional, then If-None-Match)", requests, conditional)
	}
}

func TestGetJSON_RefreshesCacheWhenResourceChanges(t *testing.T) {
	var version int32 = 1
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		v := atomic.LoadInt32(&version)
		etag := fmt.Sprintf(`"v%d"`, v)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etag)
		fmt.Fprintf(w, `{"state":"pending","total_count":%d}`, v)
	})

	s, _ := c.GetCombinedStatus(context.Background(), "o", "r", "sha")
	atomic.StoreInt32(&version, 2)
	s2, err := c.GetCombinedStatus(context.Background(), "o", "r", "sha")
	if err != nil {
		t.Fatal(err)
	}
	s3, _ := c.GetCombinedStatus(context.Background(), "o", "r", "sha")
	if s.TotalCount != 1 || s2.TotalCount != 2 || s3.TotalCount != 2 {
		t.Errorf("got %d, %d, %d; want 1, 2, 2", s.TotalCount, s2.TotalCount, s3.TotalCount)
	}
}

func TestGetJSON_CachesPerPath(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") != "" {
			t.Errorf("unexpected conditional request for %s (cache must be keyed by path)", r.URL.Path)
		}
		w.Header().Set("ETag", `"same-etag"`)
		n := strings.TrimPrefix(r.URL.Path, "/repos/o/r/pulls/")
		fmt.Fprintf(w, `{"number":%s}`, n)
	})

	a, _ := c.GetPullRequest(context.Background(), "o", "r", 1)
	b, _ := c.GetPullRequest(context.Background(), "o", "r", 2)
	if a.Number != 1 || b.Number != 2 {
		t.Errorf("got #%d and #%d", a.Number, b.Number)
	}
}

func TestGetJSON_NoETagMeansNoConditionalRequest(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") != "" {
			t.Error("must not send If-None-Match without a cached ETag")
		}
		fmt.Fprint(w, `{"number":1}`)
	})
	c.GetPullRequest(context.Background(), "o", "r", 1)
	c.GetPullRequest(context.Background(), "o", "r", 1)
}

func TestGetJSON_Unexpected304IsAnError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	})
	if _, err := c.GetPullRequest(context.Background(), "o", "r", 1); err == nil {
		t.Error("a 304 without a cached body must not decode as an empty PR")
	}
}

func TestStoreCache_IsBounded(t *testing.T) {
	c := NewClient("")
	for i := 0; i < maxCacheEntries+10; i++ {
		c.storeCache(fmt.Sprintf("/p/%d", i), cachedResponse{etag: "e"})
	}
	if n := len(c.cache); n > maxCacheEntries {
		t.Errorf("cache has %d entries, want <= %d", n, maxCacheEntries)
	}
}

func TestClient_RecordsRateLimitHeaders(t *testing.T) {
	reset := time.Now().Add(30 * time.Minute).Unix()
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Limit", "5000")
		w.Header().Set("X-RateLimit-Remaining", "4321")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset, 10))
		fmt.Fprint(w, `{"number":1}`)
	})

	if c.RateLimit().Known {
		t.Error("rate limit should be unknown before any request")
	}
	c.GetPullRequest(context.Background(), "o", "r", 1)
	rl := c.RateLimit()
	if !rl.Known || rl.Limit != 5000 || rl.Remaining != 4321 || rl.Reset.Unix() != reset {
		t.Errorf("RateLimit() = %+v", rl)
	}
}

func TestNewAPIError_DetectsRateLimits(t *testing.T) {
	reset := time.Now().Add(20 * time.Minute).Truncate(time.Second)
	tests := []struct {
		name        string
		status      int
		headers     map[string]string
		body        string
		wantLimited bool
		wantReset   func(time.Time) bool
	}{
		{
			name:        "primary limit exhausted",
			status:      403,
			headers:     map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": strconv.FormatInt(reset.Unix(), 10)},
			body:        `{"message":"API rate limit exceeded for user"}`,
			wantLimited: true,
			wantReset:   func(t time.Time) bool { return t.Equal(reset) },
		},
		{
			name:        "secondary limit with Retry-After",
			status:      403,
			headers:     map[string]string{"Retry-After": "90", "X-RateLimit-Remaining": "4000"},
			body:        `{"message":"You have exceeded a secondary rate limit"}`,
			wantLimited: true,
			wantReset: func(t time.Time) bool {
				d := time.Until(t)
				return d > 85*time.Second && d <= 90*time.Second
			},
		},
		{
			name:        "secondary limit by message only",
			status:      403,
			body:        `{"message":"You have exceeded a secondary rate limit"}`,
			wantLimited: true,
			wantReset:   func(t time.Time) bool { return t.IsZero() },
		},
		{
			name:        "429 Too Many Requests",
			status:      429,
			wantLimited: true,
			wantReset:   func(t time.Time) bool { return t.IsZero() },
		},
		{
			name:        "403 missing permission is not a rate limit",
			status:      403,
			headers:     map[string]string{"X-RateLimit-Remaining": "4999"},
			body:        `{"message":"Resource not accessible by personal access token"}`,
			wantLimited: false,
		},
		{
			name:        "404 is not a rate limit",
			status:      404,
			headers:     map[string]string{"X-RateLimit-Remaining": "0"},
			wantLimited: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &http.Response{StatusCode: tt.status, Header: make(http.Header)}
			for k, v := range tt.headers {
				resp.Header.Set(k, v)
			}
			err := newAPIError(resp, []byte(tt.body))
			if err.RateLimited != tt.wantLimited {
				t.Fatalf("RateLimited = %v, want %v", err.RateLimited, tt.wantLimited)
			}
			if tt.wantLimited && !tt.wantReset(err.ResetAt) {
				t.Errorf("unexpected ResetAt %v", err.ResetAt)
			}
			if err.StatusCode != tt.status {
				t.Errorf("StatusCode = %d", err.StatusCode)
			}
		})
	}
}

func TestIsRateLimited(t *testing.T) {
	reset := time.Now().Add(time.Hour)
	wrapped := fmt.Errorf("failed to fetch check runs: %w", &APIError{StatusCode: 403, RateLimited: true, ResetAt: reset})
	if limited, at := IsRateLimited(wrapped); !limited || !at.Equal(reset) {
		t.Errorf("IsRateLimited(wrapped) = %v, %v", limited, at)
	}
	if limited, _ := IsRateLimited(&APIError{StatusCode: 403}); limited {
		t.Error("plain 403 must not be a rate limit")
	}
	if limited, _ := IsRateLimited(errors.New("network")); limited {
		t.Error("network error must not be a rate limit")
	}
	if limited, _ := IsRateLimited(nil); limited {
		t.Error("nil must not be a rate limit")
	}
}

func TestAPIError_RateLimitMessage(t *testing.T) {
	msg := (&APIError{StatusCode: 403, RateLimited: true, ResetAt: time.Now().Add(time.Hour)}).Error()
	if !strings.Contains(msg, "rate limit exceeded") || !strings.Contains(msg, "resets at") {
		t.Errorf("unexpected message %q", msg)
	}
	if msg := (&APIError{StatusCode: 429, RateLimited: true}).Error(); strings.Contains(msg, "resets at") {
		t.Errorf("unknown reset must not be printed: %q", msg)
	}
}

func TestGetCheckRuns_RateLimitHasNoPermissionHint(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message":"API rate limit exceeded"}`)
	})
	_, err := c.GetCheckRuns(context.Background(), "o", "r", "sha")
	if limited, _ := IsRateLimited(err); !limited {
		t.Fatalf("expected a rate limit error, got %v", err)
	}
	if strings.Contains(err.Error(), "Checks: read") {
		t.Errorf("a rate limit must not be reported as a missing permission: %v", err)
	}
}

func TestClient_ConcurrentRequestsAreSafe(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"x"`)
		w.Header().Set("X-RateLimit-Limit", "5000")
		w.Header().Set("X-RateLimit-Remaining", "4999")
		w.Header().Set("X-RateLimit-Reset", "1900000000")
		fmt.Fprint(w, `{"number":1}`)
	})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			c.GetPullRequest(context.Background(), "o", "r", n%3)
			c.RateLimit()
		}(i)
	}
	wg.Wait()
}

func TestStoreCache_FullCacheKeepsOtherEntries(t *testing.T) {
	c := NewClient("")
	for i := 0; i < maxCacheEntries; i++ {
		c.storeCache(fmt.Sprintf("/p/%d", i), cachedResponse{etag: "e"})
	}
	c.storeCache("/p/0", cachedResponse{etag: "refreshed"}) // a changed resource
	if n := len(c.cache); n != maxCacheEntries || c.cache["/p/0"].etag != "refreshed" {
		t.Errorf("refreshing a cached path must not evict anything: %d entries", n)
	}
	c.storeCache("/new", cachedResponse{etag: "e"})
	if n := len(c.cache); n != maxCacheEntries {
		t.Errorf("a new path must evict exactly one entry, cache has %d", n)
	}
}

func TestStoreCache_PollingMorePathsThanFitStillHits(t *testing.T) {
	// e.g. ~200 watched PRs, 3 paths each, polled in rotation.
	c := NewClient("")
	paths := maxCacheEntries + maxCacheEntries/4
	hits, lookups := 0, 0
	for round := 0; round < 20; round++ {
		for i := 0; i < paths; i++ {
			path := fmt.Sprintf("/p/%d", i)
			if _, ok := c.cache[path]; ok {
				if round > 0 {
					hits++
				}
			} else {
				c.storeCache(path, cachedResponse{etag: "e"})
			}
			if round > 0 {
				lookups++
			}
		}
	}
	rate := float64(hits) / float64(lookups)
	t.Logf("hit rate %.0f%% polling %d paths through a %d-entry cache", rate*100, paths, maxCacheEntries)
	if rate < 0.5 {
		t.Errorf("hit rate %.0f%%; overflowing the cache must not make every request unconditional", rate*100)
	}
}
