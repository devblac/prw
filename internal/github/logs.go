package github

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
)

// maxLogBytes is how much of the end of a job log is kept. Logs can be many MB; the
// failure is near the end (only post-job cleanup follows it).
const maxLogBytes = 4 << 20

// DefaultLogLines is how many lines of a failed step an excerpt has by default.
const DefaultLogLines = 40

var (
	// actionsJobURL matches the html_url of a GitHub Actions job's check run; its
	// check run id is the job id.
	actionsJobURL = regexp.MustCompile(`/actions/runs/\d+/job/\d+`)
	logTimestamp  = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d+)?Z ?`)
	ansiEscape    = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)
	// workflowMarker matches runner markup such as ##[group], ##[endgroup],
	// ##[start-action display=...] or ##[debug].
	workflowMarker = regexp.MustCompile(`^##\[[a-z-]+[\] ]`)
)

// IsActionsJob reports whether a check run is a GitHub Actions job, whose log can be
// downloaded. Other apps' check runs (and commit statuses) have no log in the API.
func IsActionsJob(run CheckRun) bool {
	return actionsJobURL.MatchString(run.HTMLURL)
}

// GetJobLog downloads the log of a GitHub Actions job, keeping at most the last
// maxLogBytes. GitHub redirects to a short-lived storage URL; Go's client doesn't
// forward the Authorization header to it. Logs aren't cached (they're large and
// only fetched for failed jobs, which don't change).
func (c *Client) GetJobLog(ctx context.Context, owner, repo string, jobID int64) (string, error) {
	path := fmt.Sprintf("/repos/%s/%s/actions/jobs/%d/logs", owner, repo, jobID)
	req, err := http.NewRequestWithContext(ctx, "GET", c.BaseURL+path, nil)
	if err != nil {
		return "", err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	c.recordRateLimit(resp.Header)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		apiErr := newAPIError(resp, body)
		switch {
		case apiErr.StatusCode == http.StatusForbidden && !apiErr.RateLimited:
			return "", fmt.Errorf("%w (fine-grained tokens need the \"Actions: read\" permission)", apiErr)
		case apiErr.StatusCode == http.StatusNotFound || apiErr.StatusCode == http.StatusGone:
			return "", fmt.Errorf("log not available: it expired or the job hasn't finished (HTTP %d)", apiErr.StatusCode)
		}
		return "", apiErr
	}
	return readTail(resp.Body, maxLogBytes)
}

// readTail reads r to the end and returns at most its last limit bytes.
func readTail(r io.Reader, limit int) (string, error) {
	buf := make([]byte, 0, 64<<10)
	chunk := make([]byte, 64<<10)
	cut := false
	for {
		n, err := r.Read(chunk)
		buf = append(buf, chunk[:n]...)
		if len(buf) > 2*limit {
			buf = append(buf[:0], buf[len(buf)-limit:]...)
			cut = true
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("failed to read log: %w", err)
		}
	}
	if len(buf) > limit {
		buf = buf[len(buf)-limit:]
		cut = true
	}
	if cut {
		if i := strings.IndexByte(string(buf), '\n'); i >= 0 {
			buf = buf[i+1:] // drop the partial first line
		}
	}
	return string(buf), nil
}

// LogExcerpt is the interesting part of a failed job's log.
type LogExcerpt struct {
	// Step is the command or action of the failed step, e.g. "go test ./..."
	// (from its "Run ..." header); empty if it couldn't be found.
	Step string `json:"step,omitempty"`
	// Lines are the last lines the step printed, up to and including the error.
	Lines []string `json:"lines"`
	// Truncated is set when the step printed more than the lines kept.
	Truncated bool `json:"truncated,omitempty"`
}

// ExtractFailure finds the first error in a GitHub Actions job log and returns the
// output of that step leading up to it (at most maxLines lines): what a developer
// scrolls to in the web UI. Timestamps, colors and workflow commands are stripped.
// Without an error marker it returns the end of the log.
func ExtractFailure(log string, maxLines int) LogExcerpt {
	if maxLines <= 0 {
		maxLines = DefaultLogLines
	}
	raw := strings.Split(strings.ReplaceAll(log, "\r\n", "\n"), "\n")
	lines := make([]string, len(raw))
	for i, l := range raw {
		lines[i] = ansiEscape.ReplaceAllString(logTimestamp.ReplaceAllString(l, ""), "")
	}

	errAt := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "##[error]") {
			errAt = i
			break
		}
	}
	if errAt < 0 {
		end := len(lines)
		for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
			end--
		}
		start := end - maxLines
		if start < 0 {
			start = 0
		}
		return LogExcerpt{Lines: cleanLogLines(lines[start:end]), Truncated: start > 0}
	}

	// The step's output starts after its "##[group]Run <command>" header block.
	stepStart, step := 0, ""
	for i := errAt - 1; i >= 0; i-- {
		if strings.HasPrefix(lines[i], "##[group]Run ") {
			step = strings.TrimPrefix(lines[i], "##[group]Run ")
			stepStart = i + 1
			for j := i + 1; j < errAt; j++ {
				if strings.HasPrefix(lines[j], "##[endgroup]") {
					stepStart = j + 1
					break
				}
			}
			break
		}
	}
	end := errAt + 1
	for end < len(lines) && strings.HasPrefix(lines[end], "##[error]") {
		end++ // keep consecutive errors together
	}
	start := stepStart
	if end-start > maxLines {
		start = end - maxLines
	}
	return LogExcerpt{Step: step, Lines: cleanLogLines(lines[start:end]), Truncated: start > stepStart}
}

// cleanLogLines turns error/warning/notice workflow commands into readable text and
// drops the other "##[...]" markers (groups, composite action boundaries, debug).
func cleanLogLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		switch {
		case strings.HasPrefix(l, "##[error]"):
			l = "Error: " + strings.TrimPrefix(l, "##[error]")
		case strings.HasPrefix(l, "##[warning]"):
			l = "Warning: " + strings.TrimPrefix(l, "##[warning]")
		case strings.HasPrefix(l, "##[notice]"):
			l = "Notice: " + strings.TrimPrefix(l, "##[notice]")
		case workflowMarker.MatchString(l):
			continue
		}
		out = append(out, l)
	}
	return out
}

// LogFetcher downloads GitHub Actions job logs.
type LogFetcher interface {
	GetJobLog(ctx context.Context, owner, repo string, jobID int64) (string, error)
}

// FailedJobLog is the log excerpt of one failed check, or why there is none.
type FailedJobLog struct {
	Name    string      `json:"name"`
	URL     string      `json:"url,omitempty"`
	Excerpt *LogExcerpt `json:"excerpt,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// maxLogFetches bounds how many job logs one call downloads, e.g. for a matrix
// where every job failed the same way.
const maxLogFetches = 5

// FailedJobLogs returns log excerpts for the failed (not cancelled) runs among runs,
// newest run per check only. Runs that aren't GitHub Actions jobs, and failures past
// the first maxLogFetches, get an Error explaining why there's no excerpt. If ctx is
// cancelled it returns ctx.Err().
func FailedJobLogs(ctx context.Context, c LogFetcher, owner, repo string, runs []CheckRun, maxLines int) ([]FailedJobLog, error) {
	var out []FailedJobLog
	fetched := 0
	latest := LatestCheckRuns(runs)
	sorted := make([]CheckRun, len(latest))
	copy(sorted, latest)
	sort.SliceStable(sorted, func(i, j int) bool { return strings.ToLower(sorted[i].Name) < strings.ToLower(sorted[j].Name) })
	for _, run := range sorted {
		if !strings.EqualFold(run.Status, "completed") {
			continue
		}
		switch strings.ToLower(run.Conclusion) {
		case "success", "neutral", "skipped", "cancelled":
			continue
		}
		entry := FailedJobLog{Name: run.Name, URL: run.HTMLURL}
		switch {
		case !IsActionsJob(run):
			entry.Error = "not a GitHub Actions job; its log isn't available through the API"
		case fetched >= maxLogFetches:
			entry.Error = fmt.Sprintf("skipped: only the first %d failed jobs' logs are fetched", maxLogFetches)
		default:
			fetched++
			log, err := c.GetJobLog(ctx, owner, repo, run.ID)
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if err != nil {
				entry.Error = err.Error()
			} else {
				excerpt := ExtractFailure(log, maxLines)
				entry.Excerpt = &excerpt
			}
		}
		out = append(out, entry)
	}
	return out, nil
}
