package github

import "testing"

func actionsRun(id int64, name, status, conclusion string) CheckRun {
	run := CheckRun{ID: id, Name: name, Status: status, Conclusion: conclusion}
	run.App.ID = 15368 // github-actions
	return run
}

func TestSummarizeCI_IgnoresRunSupersededByNewerRun(t *testing.T) {
	// Shape of zephyrproject-rtos/zephyr#105183: a workflow with cancel-in-progress ran
	// twice for the same commit, in two check suites; the API returns both runs.
	runs := []CheckRun{
		actionsRun(66407602784, "Prevent Merging", "completed", "success"),
		actionsRun(66407483043, "Prevent Merging", "completed", "cancelled"),
		actionsRun(66407400000, "Build", "completed", "success"),
	}
	ci := SummarizeCI(&CombinedStatus{State: "pending"}, runs)
	if ci.State != "success" || len(ci.FailedChecks) != 0 || ci.Total != 2 {
		t.Errorf("got state=%q failed=%v total=%d, want success with 2 checks", ci.State, ci.FailedChecks, ci.Total)
	}
}

func TestSummarizeCI_NewerFailureStillCounts(t *testing.T) {
	runs := []CheckRun{
		actionsRun(1, "Test", "completed", "success"),
		actionsRun(2, "Test", "completed", "failure"),
	}
	if ci := SummarizeCI(nil, runs); ci.State != "failure" || len(ci.FailedChecks) != 1 {
		t.Errorf("got state=%q failed=%v, want the newer failure", ci.State, ci.FailedChecks)
	}
}

func TestSummarizeCI_NewerRunStillInProgress(t *testing.T) {
	runs := []CheckRun{
		actionsRun(1, "Test", "completed", "failure"),
		actionsRun(2, "Test", "in_progress", ""),
	}
	if ci := SummarizeCI(nil, runs); ci.State != "pending" || ci.Pending != 1 || len(ci.FailedChecks) != 0 {
		t.Errorf("got state=%q pending=%d failed=%v, want pending on the re-run", ci.State, ci.Pending, ci.FailedChecks)
	}
}

func TestSummarizeCI_SameNameFromDifferentAppsIsKept(t *testing.T) {
	other := CheckRun{ID: 1, Name: "Test", Status: "completed", Conclusion: "failure"}
	other.App.ID = 999 // e.g. an external CI that also names its check "Test"
	runs := []CheckRun{other, actionsRun(2, "Test", "completed", "success")}
	if ci := SummarizeCI(nil, runs); ci.State != "failure" {
		t.Errorf("state = %q; a failure from another app must not be hidden", ci.State)
	}
}
