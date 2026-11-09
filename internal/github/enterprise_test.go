package github

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

func TestEnterpriseAPIURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.acme.com":       "https://github.acme.com/api/v3",
		"github.acme.com/":              "https://github.acme.com/api/v3",
		"http://ghe.local:8080":         "http://ghe.local:8080/api/v3",
		"https://Octocorp.GHE.com":      "https://api.octocorp.ghe.com",
		" https://github.acme.com/x/y ": "https://github.acme.com/api/v3",
	} {
		if got, err := EnterpriseAPIURL(in); err != nil || got != want {
			t.Errorf("EnterpriseAPIURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "ftp://github.acme.com", "https://"} {
		if _, err := EnterpriseAPIURL(bad); err == nil {
			t.Errorf("EnterpriseAPIURL(%q) should fail", bad)
		}
	}
	c, err := NewEnterpriseClient("https://github.acme.com", "t")
	if err != nil || c.BaseURL != "https://github.acme.com/api/v3" || c.Token != "t" || c.Service != "GitHub Enterprise" {
		t.Errorf("client = %+v, %v", c, err)
	}
}

func TestSearchPullRequests_EnterpriseResults(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"total_count":1,"incomplete_results":false,"items":[{"number":9,"title":"Add SSO","html_url":"https://github.acme.com/team/api/pull/9","pull_request":{"url":"x"}}]}`)
	})
	res, err := c.SearchPullRequests(context.Background(), "is:pr")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.PRs) != 1 || res.PRs[0].Owner != "team" || res.PRs[0].Repo != "api" || res.PRs[0].Number != 9 {
		t.Errorf("result = %+v", res)
	}
}
