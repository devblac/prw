package bitbucket

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/devblac/prw/internal/github"
)

// maxStepLogBytes is how much of the end of a step log is read: the failure is there.
const maxStepLogBytes = 4 << 20

var (
	// pipelineResultURL matches the link of a Pipelines build status, e.g.
	// https://bitbucket.org/ws/repo/addon/pipelines/home#!/results/2435 or
	// https://bitbucket.org/ws/repo/pipelines/results/2435.
	pipelineResultURL = regexp.MustCompile(`/pipelines/(?:home#!/)?results/(\d+)`)
	ansiEscape        = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)
	// teardownLine starts the output Pipelines adds after the script.
	teardownLine = regexp.MustCompile(`^(Searching for test report files|Finished scanning for test reports|Merged test suites|Searching for files matching artifact pattern)`)
)

func pipelineBuildNumber(run github.CheckRun) (int, bool) {
	m := pipelineResultURL.FindStringSubmatch(run.HTMLURL)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	return n, err == nil
}

// CanReadLog reports whether run is a Bitbucket Pipelines build, whose log prw reads.
func (c *Client) CanReadLog(run github.CheckRun) bool {
	_, ok := pipelineBuildNumber(run)
	return ok
}

// NoLogReason explains why other builds (Jenkins, ...) have no excerpt.
func (c *Client) NoLogReason() string {
	return "not a Bitbucket Pipelines build; its log isn't available through the Bitbucket API"
}

type pipelineStep struct {
	UUID  string `json:"uuid"`
	Name  string `json:"name"`
	State struct {
		Result struct {
			Name  string `json:"name"` // SUCCESSFUL, FAILED, ERROR, STOPPED, NOT_RUN
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		} `json:"result"`
	} `json:"state"`
	Pipeline struct {
		UUID string `json:"uuid"`
	} `json:"pipeline"`
	ScriptCommands []struct {
		Name string `json:"name"`
	} `json:"script_commands"`
}

// RunLog returns the output of the command that failed in the first failed step of a
// Pipelines build. Reading logs needs a token with the pipeline read scope.
func (c *Client) RunLog(ctx context.Context, workspace, repo string, run github.CheckRun, maxLines int) (github.LogExcerpt, error) {
	number, ok := pipelineBuildNumber(run)
	if !ok {
		return github.LogExcerpt{}, errors.New(c.NoLogReason())
	}
	var resp struct {
		Values []pipelineStep `json:"values"`
	}
	path := fmt.Sprintf("%s/pipelines/%d/steps/?pagelen=100", repoPath(workspace, repo), number)
	if err := c.http.GetJSON(ctx, path, &resp); err != nil {
		return github.LogExcerpt{}, fmt.Errorf("failed to list the steps of pipeline #%d: %w", number, err)
	}
	var step *pipelineStep
	for i := range resp.Values {
		switch strings.ToUpper(resp.Values[i].State.Result.Name) {
		case "FAILED", "ERROR":
			step = &resp.Values[i]
		}
		if step != nil {
			break
		}
	}
	if step == nil {
		return github.LogExcerpt{}, fmt.Errorf("pipeline #%d has no failed step", number)
	}
	reason := step.State.Result.Error.Message // e.g. "Exceeded build time limit of 120 minutes."

	logPath := fmt.Sprintf("%s/pipelines/%s/steps/%s/log", repoPath(workspace, repo), url.PathEscape(step.Pipeline.UUID), url.PathEscape(step.UUID))
	log, err := c.http.GetText(ctx, logPath, maxStepLogBytes)
	if err != nil {
		var apiErr *github.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			err = errors.New("log not available: reading Pipelines logs needs a Bitbucket token with the pipeline read scope, or the log expired")
		}
		if reason != "" {
			return github.LogExcerpt{Step: step.Name, Lines: []string{reason}}, nil
		}
		return github.LogExcerpt{}, err
	}
	var commands []string
	for _, cmd := range step.ScriptCommands {
		commands = append(commands, cmd.Name)
	}
	excerpt := ExtractStepFailure(log, commands, maxLines)
	if reason != "" {
		excerpt.Lines = append(excerpt.Lines, reason)
	}
	return excerpt, nil
}

// ExtractStepFailure returns the output of the last script command a Pipelines step
// ran — the one that failed, since a step stops at the first failing command — up to
// the teardown, at most maxLines lines. Pipelines echoes each command as "+ <command>".
// Without such a line it returns the end of the log.
func ExtractStepFailure(log string, commands []string, maxLines int) github.LogExcerpt {
	if maxLines <= 0 {
		maxLines = github.DefaultLogLines
	}
	raw := strings.Split(strings.ReplaceAll(log, "\r\n", "\n"), "\n")
	lines := make([]string, len(raw))
	for i, l := range raw {
		lines[i] = strings.TrimRight(ansiEscape.ReplaceAllString(l, ""), "\r")
	}

	known := map[string]bool{}
	for _, c := range commands {
		// A multi-line command is echoed with its first line.
		first, _, _ := strings.Cut(c, "\n")
		known[strings.TrimSpace(first)] = true
	}
	start, step := -1, ""
	for i := len(lines) - 1; i >= 0 && start < 0; i-- {
		if cmd, ok := strings.CutPrefix(lines[i], "+ "); ok && known[strings.TrimSpace(cmd)] {
			start, step = i+1, strings.TrimSpace(cmd)
		}
	}
	if start < 0 { // commands unknown or not echoed: fall back to any "+ " line
		for i := len(lines) - 1; i >= 0 && start < 0; i-- {
			if cmd, ok := strings.CutPrefix(lines[i], "+ "); ok {
				start, step = i+1, strings.TrimSpace(cmd)
			}
		}
	}
	if start < 0 {
		start = 0
	}

	end := len(lines)
	for i := start; i < len(lines); i++ {
		if teardownLine.MatchString(lines[i]) {
			end = i
			break
		}
	}
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	from := end - maxLines
	if from < start {
		from = start
	}
	out := append([]string{}, lines[from:end]...)
	return github.LogExcerpt{Step: step, Lines: out, Truncated: from > start}
}
