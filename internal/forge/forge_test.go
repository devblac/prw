package forge

import "testing"

func TestParsePRURL(t *testing.T) {
	tests := []struct {
		url  string
		want Ref
	}{
		{"https://github.com/owner/repo/pull/123", Ref{GitHub, "owner", "repo", 123}},
		{"github.com/owner/repo/pull/7/files", Ref{GitHub, "owner", "repo", 7}},
		{"https://bitbucket.org/acme/api/pull-requests/42", Ref{Bitbucket, "acme", "api", 42}},
		{"https://bitbucket.org/acme/api/pull-requests/42/diff", Ref{Bitbucket, "acme", "api", 42}},
		{"bitbucket.org/acme/web-app/pull-requests/1", Ref{Bitbucket, "acme", "web-app", 1}},
	}
	for _, tt := range tests {
		got, err := ParsePRURL(tt.url)
		if err != nil || got != tt.want {
			t.Errorf("ParsePRURL(%q) = %+v, %v; want %+v", tt.url, got, err, tt.want)
		}
	}
	for _, bad := range []string{"", "https://gitlab.com/o/r/-/merge_requests/1", "https://bitbucket.org/acme/api/pull/42", "https://github.com/o/r/pull-requests/1", "https://evil.com/github.com/o/r/pull/1"} {
		if _, err := ParsePRURL(bad); err == nil {
			t.Errorf("ParsePRURL(%q) should fail", bad)
		}
	}
}

func TestPRURLAndHosts(t *testing.T) {
	if got := PRURL("", "o", "r", 1); got != "https://github.com/o/r/pull/1" {
		t.Errorf("empty host (old watch-list entries) must mean GitHub: %s", got)
	}
	if got := (Ref{Bitbucket, "acme", "api", 42}).URL(); got != "https://bitbucket.org/acme/api/pull-requests/42" {
		t.Errorf("bitbucket URL = %s", got)
	}
	if StorageHost(GitHub) != "" || StorageHost("Bitbucket.org") != Bitbucket || !IsBitbucket("BITBUCKET.ORG") || IsBitbucket("") {
		t.Error("host normalization")
	}
}
