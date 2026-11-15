package promotedriver

// lesson_index_status.go is the hook-index rebuild every lesson-changing
// command runs after its ledger write. The index belongs to hook; this file
// checks the rebuilt index serves (or no longer serves) the lesson the command
// changed. A failed rebuild never undoes the ledger change: the command says
// that serving is stale, names the repair command, and exits non-zero.

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
	// Served is true when the lesson the command wrote or changed is in the
	// index (false for a retired lesson or one without a trigger).
	Served  bool   `json:"served"`
	LeftOut int    `json:"left_out,omitempty"`
	Error   string `json:"error,omitempty"`
	Repair  string `json:"repair,omitempty"`
	summary string
}

// rebuildLessonIndexFor rebuilds the home's index and checks it serves want
// and does not serve any of gone. The error explains a stale or wrong index.
func rebuildLessonIndexFor(homePath, want string, gone []string, now time.Time) (LessonIndexStatus, error) {
	status := LessonIndexStatus{Path: hook.LessonIndexPath(homePath)}
	build, err := hook.RebuildLessonIndex(homePath, now)
	if err != nil {
		status.Error, status.Repair = err.Error(), hook.LessonIndexRepairCommand
		return status, fmt.Errorf("the hook index was not rebuilt: %v; serving is stale until `%s` succeeds", err, hook.LessonIndexRepairCommand)
	}
	status.Rebuilt, status.Lessons, status.LeftOut = true, len(build.Index.Lessons), len(build.Problems)
	status.summary = hook.LessonIndexSummary(build)
	if want != "" {
		status.Served = build.Serves(want)
		if !status.Served {
			reason := "it was left out of the index"
			for _, problem := range build.Problems {
				if strings.Contains(problem.Error(), want) {
					reason = problem.Error()
				}
			}
			status.Error = reason
			return status, fmt.Errorf("the hook will not serve lesson %s: %s; fix it with `compound memory lesson amend %s ...`", want, reason, want)
		}
	}
	for _, id := range gone {
		if build.Serves(id) {
			status.Error = "still served: " + id
			return status, fmt.Errorf("the rebuilt hook index still serves lesson %s", id)
		}
	}
	return status, nil
}

func lessonServedWhenCurrent(claim mem.Claim) bool {
	_, present, err := mem.ParseLessonTrigger(claim.Qualifiers["trigger"])
	return err == nil && present && !strings.EqualFold(claim.Visibility, "retired")
}
