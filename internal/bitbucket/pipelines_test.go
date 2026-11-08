package bitbucket

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/devblac/prw/internal/github"
)

// stepLog is shaped like a Pipelines step log: setup commands, then each script
// command echoed as "+ <command>" with its output, then the teardown.
const stepLog = "+ umask 000\n" +
	"+ retry 6 $CHECKOUT_SCRIPT\n" +
	"Initialized empty Git repository\n" +
	"+ npm install\n" +
	"added 812 packages in 21s\n" +
	"+ npm test\n" +
	"\x1b[32m> atlassian-connect-express@12.0.0 test\x1b[0m\n" +
	"> jest\r\n" +
	"FAIL test/store.test.js\n" +
	"  ● store › saves a client\n" +
	"    expected 1, received 0\n" +
	"\n" +
	"Searching for test report files in directories named [test-results, failsafe-reports] recursively\n" +
	"Finished scanning for test reports. Found 0 test report files.\n" +
	"Merged test suites, total number tests is 0, with 0 failures and 0 errors.\n"

func TestExtractStepFailure(t *testing.T) {
	got := ExtractStepFailure(stepLog, []string{"npm install", "npm test", "npm run lint:all"}, 50)
	if got.Step != "npm test" || got.Truncated {
		t.Errorf("step = %q, truncated = %v", got.Step, got.Truncated)
	}
	want := "> atlassian-connect-express@12.0.0 test|> jest|FAIL test/store.test.js|  ● store › saves a client|    expected 1, received 0"
	if s := strings.Join(got.Lines, "|"); s != want {
		t.Errorf("lines =\n%s\nwant\n%s", s, want)
	}

	short := ExtractStepFailure(stepLog, []string{"npm test"}, 2)
	if !short.Truncated || len(short.Lines) != 2 || short.Lines[1] != "    expected 1, received 0" {
		t.Errorf("short = %+v", short)
	}

	// Commands unknown: the last echoed command is used.
	if got := ExtractStepFailure(stepLog, nil, 50); got.Step != "npm test" {
		t.Errorf("without commands: step = %q", got.Step)
	}
	// Nothing echoed: the end of the log.
	if got := ExtractStepFailure("one\ntwo\nthree\n", nil, 2); strings.Join(got.Lines, "|") != "two|three" || !got.Truncated {
		t.Errorf("plain log = %+v", got)
	}
}

const stepsJSON = `{"values": [
  {"uuid": "{s1}", "name": "Build", "state": {"result": {"name": "SUCCESSFUL"}}, "pipeline": {"uuid": "{p1}"}},
  {"uuid": "{s2}", "name": "Test", "state": {"result": {"name": "FAILED"%s}}, "pipeline": {"uuid": "{p1}"},
   "script_commands": [{"name": "npm install"}, {"name": "npm test"}]}
]}`

func newPipelinesFake(t *testing.T, stepError string, logStatus int) (*Client, *[]string) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.EscapedPath())
		switch r.URL.EscapedPath() {
		case "/repositories/acme/api/pipelines/2435/steps/":
			fmt.Fprintf(w, stepsJSON, stepError)
		case "/repositories/acme/api/pipelines/%7Bp1%7D/steps/%7Bs2%7D/log":
			if logStatus != http.StatusOK {
				w.WriteHeader(logStatus)
				fmt.Fprint(w, `{"error": {"message": "Not found"}}`)
				return
			}
			fmt.Fprint(w, stepLog)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	c := NewClient("me@example.com", "token")
	c.SetBaseURL(srv.URL)
	return c, &paths
}

func pipelineRun(name, url string) github.CheckRun {
	return github.CheckRun{ID: 1, Name: name, HTMLURL: url, Status: "completed", Conclusion: "failure"}
}

func TestRunLog(t *testing.T) {
	c, paths := newPipelinesFake(t, "", http.StatusOK)
	run := pipelineRun("Pipeline #2435 for main", "https://bitbucket.org/acme/api/addon/pipelines/home#!/results/2435")
	if !c.CanReadLog(run) || c.CanReadLog(pipelineRun("Jenkins", "https://ci.example.com/job/42")) {
		t.Fatal("CanReadLog must accept Pipelines links only")
	}
	if !c.CanReadLog(pipelineRun("new link", "https://bitbucket.org/acme/api/pipelines/results/2435")) {
		t.Error("CanReadLog must accept the newer /pipelines/results/N links")
	}

	got, err := c.RunLog(context.Background(), "acme", "api", run, 50)
	if err != nil {
		t.Fatalf("%v (requests: %v)", err, *paths)
	}
	if got.Step != "npm test" || got.Lines[len(got.Lines)-1] != "    expected 1, received 0" {
		t.Errorf("excerpt = %+v", got)
	}
	if c.RequestsInLastHour() != 2 {
		t.Errorf("requests = %d, want 2 (steps + log)", c.RequestsInLastHour())
	}
}

func TestRunLog_NoLog(t *testing.T) {
	c, _ := newPipelinesFake(t, "", http.StatusNotFound)
	run := pipelineRun("p", "https://bitbucket.org/acme/api/addon/pipelines/home#!/results/2435")
	if _, err := c.RunLog(context.Background(), "acme", "api", run, 50); err == nil || !strings.Contains(err.Error(), "pipeline read scope") {
		t.Errorf("err = %v, want a hint about the token scope", err)
	}

	// A step that failed without a log (e.g. a timeout) still says why.
	c, _ = newPipelinesFake(t, `, "error": {"message": "Exceeded build time limit of 120 minutes."}`, http.StatusNotFound)
	got, err := c.RunLog(context.Background(), "acme", "api", run, 50)
	if err != nil || strings.Join(got.Lines, "") != "Exceeded build time limit of 120 minutes." {
		t.Errorf("got %+v, %v", got, err)
	}
}

func TestFailedJobLogs_UsesPipelinesLogs(t *testing.T) {
	c, _ := newPipelinesFake(t, "", http.StatusOK)
	runs := []github.CheckRun{
		pipelineRun("Pipeline #2435 for main", "https://bitbucket.org/acme/api/addon/pipelines/home#!/results/2435"),
		pipelineRun("Jenkins", "https://ci.example.com/job/42"),
	}
	runs[1].ID = 2
	logs, err := github.FailedJobLogs(context.Background(), c, "acme", "api", runs, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 2 {
		t.Fatalf("logs = %+v", logs)
	}
	if logs[0].Name != "Jenkins" || !strings.Contains(logs[0].Error, "not a Bitbucket Pipelines build") {
		t.Errorf("Jenkins entry = %+v", logs[0])
	}
	if logs[1].Excerpt == nil || logs[1].Excerpt.Step != "npm test" {
		t.Errorf("Pipelines entry = %+v", logs[1])
	}
}
