package github

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func searchItem(owner, repo string, number int, isPR bool) string {
	pr := ""
	if isPR {
		pr = `,"pull_request":{"url":"x"}`
	}
	return fmt.Sprintf(`{"number":%d,"title":"%s %d","html_url":"https://github.com/%s/%s/pull/%d"%s}`, number, repo, number, owner, repo, number, pr)
}

func TestSearchPullRequests_ParsesResultsAndSkipsIssues(t *testing.T) {
	var gotQuery string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/issues" {
			t.Errorf("path = %s", r.URL.Path)
		}
		gotQuery = r.URL.Query().Get("q")
		fmt.Fprintf(w, `{"total_count":3,"incomplete_results":false,"items":[%s,%s,%s]}`,
			searchItem("Alice", "api", 1, true), searchItem("bob", "web", 2, false), searchItem("bob", "web", 3, true))
	})

	res, err := c.SearchPullRequests(context.Background(), "is:pr is:open author:@me")
	if err != nil {
		t.Fatal(err)
	}
	if gotQuery != "is:pr is:open author:@me" {
		t.Errorf("query sent = %q (must round-trip through URL encoding)", gotQuery)
	}
	if len(res.PRs) != 2 || res.Incomplete {
		t.Fatalf("result = %+v", res)
	}
	if p := res.PRs[0]; p.Owner != "Alice" || p.Repo != "api" || p.Number != 1 || p.Title != "api 1" {
		t.Errorf("first PR = %+v", p)
	}
	if p := res.PRs[1]; p.Number != 3 {
		t.Errorf("an issue must be skipped, got %+v", p)
	}
}

func TestSearchPullRequests_Paginates(t *testing.T) {
	var pages []string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		pages = append(pages, page)
		n, _ := strconv.Atoi(page)
		count := searchPerPage
		if n == 2 {
			count = 5
		}
		items := make([]string, count)
		for i := range items {
			items[i] = searchItem("o", "r", n*1000+i, true)
		}
		fmt.Fprintf(w, `{"total_count":%d,"items":[%s]}`, searchPerPage+5, strings.Join(items, ","))
	})

	res, err := c.SearchPullRequests(context.Background(), "q")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.PRs) != searchPerPage+5 || len(pages) != 2 || res.Incomplete {
		t.Errorf("got %d PRs over pages %v, incomplete=%v", len(res.PRs), pages, res.Incomplete)
	}
}

func TestSearchPullRequests_CapsAndMarksIncomplete(t *testing.T) {
	requests := 0
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		items := make([]string, searchPerPage)
		for i := range items {
			items[i] = searchItem("o", "r", requests*1000+i, true)
		}
		fmt.Fprintf(w, `{"total_count":5000,"items":[%s]}`, strings.Join(items, ","))
	})

	res, err := c.SearchPullRequests(context.Background(), "q")
	if err != nil {
		t.Fatal(err)
	}
	if requests != maxSearchPages || len(res.PRs) != maxSearchPages*searchPerPage {
		t.Errorf("requests=%d PRs=%d", requests, len(res.PRs))
	}
	if !res.Incomplete || res.Total != 5000 {
		t.Errorf("capped results must be marked incomplete: %+v", res.Total)
	}
}

func TestSearchPullRequests_IncompleteFromGitHub(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"total_count":1,"incomplete_results":true,"items":[%s]}`, searchItem("o", "r", 1, true))
	})
	res, err := c.SearchPullRequests(context.Background(), "q")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Incomplete {
		t.Error("incomplete_results from GitHub must be propagated")
	}
}

func TestSearchPullRequests_Error(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Validation Failed"}`, http.StatusUnprocessableEntity)
	})
	if _, err := c.SearchPullRequests(context.Background(), "q"); err == nil || !strings.Contains(err.Error(), "422") {
		t.Errorf("expected a 422 error, got %v", err)
	}
}
