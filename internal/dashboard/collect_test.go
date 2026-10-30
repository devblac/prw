package dashboard

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devblac/prw/internal/forge"
)

// fakeSource returns items and counts CI reads per PR.
type fakeSource struct {
	mu       sync.Mutex
	items    []Item
	ci       map[int]string // number → CI state Enrich reports
	ciReads  map[int]int
	failCI   bool
	discover error
}

func (f *fakeSource) Name() string            { return "Fake" }
func (f *fakeSource) RequestsInLastHour() int { return 7 }
func (f *fakeSource) Discover(ctx context.Context, _ Filter) ([]Item, []string, error) {
	return append([]Item(nil), f.items...), nil, f.discover
}
func (f *fakeSource) Enrich(ctx context.Context, it *Item, needCI bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if needCI {
		f.ciReads[it.Number]++
		if f.failCI {
			return errors.New("boom")
		}
		it.CI = f.ci[it.Number]
	}
	it.Review = "none"
	return nil
}

func pr(number int, sha, state string) Item {
	ref := forge.Ref{Host: forge.Bitbucket, Owner: "ws", Repo: "api", Number: number}
	return Item{Ref: ref, Repo: "ws/api", Number: number, HeadSHA: sha, State: state, Target: "dev"}
}

func newFake(items ...Item) *fakeSource {
	return &fakeSource{items: items, ci: map[int]string{}, ciReads: map[int]int{}}
}

func TestCollector_ReadsCIOnlyWhenItMayHaveChanged(t *testing.T) {
	src := newFake(pr(1, "a", StateOpen), pr(2, "b", StateOpen), pr(3, "c", StateMerged))
	src.ci = map[int]string{1: "success", 2: "pending"}
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	c := NewCollector(src)
	c.Now = func() time.Time { return now }

	items, warnings := c.Refresh(context.Background(), Filter{Mine: true})
	if len(warnings) != 0 || items[0].CI != "success" || items[1].CI != "pending" {
		t.Fatalf("items=%+v warnings=%v", items, warnings)
	}
	if src.ciReads[3] != 0 {
		t.Error("merged PRs have no CI to read")
	}

	now = now.Add(time.Minute)
	items, _ = c.Refresh(context.Background(), Filter{Mine: true})
	if src.ciReads[1] != 1 || src.ciReads[2] != 2 {
		t.Errorf("reads after a minute: finished=%d running=%d; want 1 (cached) and 2 (still running)", src.ciReads[1], src.ciReads[2])
	}
	if items[0].CI != "success" {
		t.Errorf("cached CI must still be shown: %+v", items[0])
	}

	src.items[0].HeadSHA = "a2" // new push
	c.Refresh(context.Background(), Filter{Mine: true})
	if src.ciReads[1] != 2 {
		t.Errorf("a new head must be read: %d reads", src.ciReads[1])
	}

	now = now.Add(DefaultCIRecheck)
	c.Refresh(context.Background(), Filter{Mine: true})
	if src.ciReads[1] != 3 {
		t.Errorf("finished CI must be re-read after %s (a build may have been re-run): %d reads", DefaultCIRecheck, src.ciReads[1])
	}
}

func TestCollector_FailedCIReadKeepsLastKnownState(t *testing.T) {
	src := newFake(pr(1, "a", StateOpen))
	src.ci = map[int]string{1: "pending"}
	c := NewCollector(src)
	c.Refresh(context.Background(), Filter{Mine: true})

	src.failCI = true
	items, warnings := c.Refresh(context.Background(), Filter{Mine: true})
	if items[0].CI != "pending" || len(warnings) != 1 || !strings.Contains(warnings[0], "boom") {
		t.Errorf("items=%+v warnings=%v", items, warnings)
	}
}

func TestCollector_SourceFailureIsAWarning(t *testing.T) {
	bad := newFake()
	bad.discover = errors.New("401 Unauthorized")
	good := newFake(pr(1, "a", StateOpen))
	items, warnings := NewCollector(bad, good).Refresh(context.Background(), Filter{Mine: true})
	if len(items) != 1 || len(warnings) != 1 || !strings.Contains(warnings[0], "Fake: 401") {
		t.Errorf("items=%d warnings=%v", len(items), warnings)
	}
}

func TestCollector_Dedupes(t *testing.T) {
	mine := pr(1, "a", StateOpen)
	review := mine
	review.Role = RoleReview
	items, _ := NewCollector(newFake(mine, review)).Refresh(context.Background(), Filter{Mine: true, Review: true})
	if len(items) != 1 {
		t.Errorf("items = %d, want the PR once", len(items))
	}
}

func TestCollector_Requests(t *testing.T) {
	if got := strings.Join(NewCollector(newFake()).Requests(), ","); got != "Fake 7/h" {
		t.Errorf("Requests = %s", got)
	}
}
