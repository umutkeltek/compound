package promotedriver

// lesson_index_status.go is the hook-index rebuild every lesson-changing
// command runs after its ledger write. The index belongs to hook; this file
// checks the rebuilt index carries (or no longer carries) the lesson the
// command changed. A failed rebuild never undoes the ledger change: the
// command says that serving is stale, names the repair command, and exits
// non-zero. A lesson that must stop being served (retire, the old version of
// an amend) is removed from the published index even when the full rebuild
// fails.

import (
	"fmt"
	"strings"
	"time"

	"compound/internal/hook"
	"compound/internal/mem"
)

// LessonIndexStatus reports the hook index a lesson command rebuilt.
type LessonIndexStatus struct {
	Path    string `json:"path,omitempty"`
	Rebuilt bool   `json:"rebuilt"`
	Lessons int    `json:"lessons"`
	// Indexed is true when the lesson the command wrote or changed is in the
	// published index, and Rank is its position there: a tool call serves
	// the first two matching lessons in rank order, so an indexed lesson is
	// served when it is among the first two matches. False for a retired
	// lesson, one without a trigger, or one left out because the index is full.
	Indexed bool `json:"indexed"`
	Rank    int  `json:"rank,omitempty"`
	// Unserved are lessons left out because the index is full.
	Unserved    []string `json:"unserved,omitempty"`
	FillPercent int      `json:"fill_percent"`
	LeftOut     int      `json:"left_out,omitempty"`
	// Removed lists lessons taken out of the published index after a failed
	// full rebuild.
	Removed []string `json:"removed,omitempty"`
	Warning string   `json:"warning,omitempty"`
	Error   string   `json:"error,omitempty"`
	Repair  string   `json:"repair,omitempty"`
	summary string
}

// rebuildLessonIndexFor rebuilds the home's index and checks it carries want
// and none of gone. The error explains a stale, full or wrong index; a nil
// error with a Warning means the command's own change took effect.
func rebuildLessonIndexFor(homePath, want string, gone []string, now time.Time) (LessonIndexStatus, error) {
	status := LessonIndexStatus{Path: hook.LessonIndexPath(homePath)}
	build, err := hook.RebuildLessonIndex(homePath, now)
	if err != nil {
		status.Error, status.Repair = err.Error(), hook.LessonIndexRepairCommand
		var removeErr error
		if len(gone) > 0 {
			status.Removed, removeErr = hook.RemoveLessonsFromIndex(homePath, gone)
		}
		switch {
		case removeErr != nil:
			return status, fmt.Errorf("the hook index was not rebuilt (%v) and the old lesson could not be removed from it (%v); serving is stale until `%s` succeeds", err, removeErr, hook.LessonIndexRepairCommand)
		case want == "" && len(gone) > 0:
			// A retire: the lesson is out of the published index, which is
			// all the command promised.
			status.Warning = fmt.Sprintf("the full hook index rebuild failed (%v); the retired lesson was removed from the published index, so it is not served; run `%s` to repair the rest", err, hook.LessonIndexRepairCommand)
			return status, nil
		}
		return status, fmt.Errorf("the hook index was not rebuilt: %v; serving is stale until `%s` succeeds", err, hook.LessonIndexRepairCommand)
	}
	status.Rebuilt, status.Lessons, status.LeftOut = true, len(build.Index.Lessons), len(build.Problems)
	status.Unserved, status.FillPercent = build.Unserved, build.FillPercent()
	status.summary = hook.LessonIndexSummary(build)
	status.Warning = hook.LessonIndexFullAdvice(build)
	for _, id := range gone {
		if build.Serves(id) {
			status.Error = "still served: " + id
			return status, fmt.Errorf("the rebuilt hook index still serves lesson %s", id)
		}
	}
	if want == "" {
		return status, nil
	}
	status.Rank = build.Rank(want)
	status.Indexed = status.Rank > 0
	if !status.Indexed {
		reason, fix := "it was left out of the index", fmt.Sprintf("fix it with `compound memory lesson amend %s ...`", want)
		for _, id := range build.Unserved {
			if id == want {
				reason, fix = "the index is full", status.Warning
			}
		}
		for _, problem := range build.Problems {
			if strings.Contains(problem.Error(), want) {
				reason = problem.Error()
			}
		}
		status.Error = reason
		return status, fmt.Errorf("the hook will not serve lesson %s: %s; %s", want, reason, fix)
	}
	if len(build.Unserved) > 0 {
		// This lesson fit, but older ones no longer do: the write that filled
		// the index exits non-zero so its author sees it.
		status.Error = "index full"
		return status, fmt.Errorf("lesson %s is indexed, but %s", want, status.Warning)
	}
	return status, nil
}

func lessonServedWhenCurrent(claim mem.Claim) bool {
	_, present, err := mem.ParseLessonTrigger(claim.Qualifiers["trigger"])
	return err == nil && present && !strings.EqualFold(claim.Visibility, "retired")
}
