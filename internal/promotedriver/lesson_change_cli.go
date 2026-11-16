package promotedriver

// lesson_change_cli.go owns `compound memory lesson retire|amend|restore`:
// one step each, no carrier work item. The ledger change belongs to mem; the
// index rebuild that follows it is lesson_index_status.go.

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"compound/internal/mem"
)

// LessonChangeUsage is the usage text for the lesson verbs.
const LessonChangeUsage = "usage: compound memory lesson retire <claim_id> --reason TEXT --by ACTOR [--work W] [--json]\n" +
	"       compound memory lesson amend <claim_id> [--how TEXT] [--trigger TOOL:REGEX [--allow-broad-trigger] | --no-trigger] [--reason TEXT] --by ACTOR [--work W] [--json]\n" +
	"       compound memory lesson restore <claim_id> [--reason TEXT] --by ACTOR [--work W] [--json]\n" +
	"  One step each, no work item needed. Any id in the lesson's version chain works. Each change is a new version and\n" +
	"  the old one stays as history; the hook index is rebuilt in the same command. Undo: retire <-> restore; amend back."

// LessonChangeReceipt is the JSON receipt of one lesson verb.
type LessonChangeReceipt struct {
	mem.LessonChangeResult
	Undo  string            `json:"undo"`
	Index LessonIndexStatus `json:"index"`
}

type lessonChangeArgs struct {
	in   mem.LessonChangeInput
	json bool
}

func parseLessonChangeArgs(verb string, args []string) (lessonChangeArgs, error) {
	var out lessonChangeArgs
	value := func(i *int, name string) (string, error) {
		if *i+1 >= len(args) {
			return "", fmt.Errorf("%s needs a value", name)
		}
		*i++
		return args[*i], nil
	}
	for i := 0; i < len(args); i++ {
		var err error
		switch a := args[i]; {
		case a == "--by":
			out.in.Actor, err = value(&i, a)
		case a == "--reason":
			out.in.Reason, err = value(&i, a)
		case a == "--work":
			out.in.WorkID, err = value(&i, a)
		case a == "--json":
			out.json = true
		case a == "--how" && verb == mem.LessonChangeAmended:
			out.in.How, err = value(&i, a)
		case a == "--trigger" && verb == mem.LessonChangeAmended:
			out.in.Trigger, err = value(&i, a)
		case a == "--no-trigger" && verb == mem.LessonChangeAmended:
			out.in.DropTrigger = true
		case a == "--allow-broad-trigger" && verb == mem.LessonChangeAmended:
			out.in.AllowBroadTrigger = true
		case !strings.HasPrefix(a, "-") && out.in.ID == "":
			out.in.ID = a
		default:
			return out, fmt.Errorf("unknown argument %q", a)
		}
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

var lessonVerbs = map[string]string{
	"retire":  mem.LessonChangeRetired,
	"amend":   mem.LessonChangeAmended,
	"restore": mem.LessonChangeRestored,
}

// RunLessonChangeCLI runs `compound memory lesson retire|amend|restore`.
func RunLessonChangeCLI(store *mem.Store, verb string, args []string, stdout, stderr io.Writer, now time.Time) int {
	change, ok := lessonVerbs[verb]
	if !ok {
		fmt.Fprintln(stderr, LessonChangeUsage)
		return 2
	}
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			fmt.Fprintln(stdout, LessonChangeUsage)
			return 0
		}
	}
	parsed, err := parseLessonChangeArgs(change, args)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		fmt.Fprintln(stderr, LessonChangeUsage)
		return 2
	}
	var res mem.LessonChangeResult
	switch change {
	case mem.LessonChangeRetired:
		res, err = store.RetireLesson(parsed.in, now)
	case mem.LessonChangeAmended:
		res, err = store.AmendLesson(parsed.in, now)
	default:
		res, err = store.RestoreLesson(parsed.in, now)
	}
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	receipt := LessonChangeReceipt{LessonChangeResult: res, Undo: lessonChangeUndo(res, parsed.in.Actor)}
	want := ""
	if lessonServedWhenCurrent(res.Claim) {
		want = res.Claim.ID
	}
	gone := []string{res.Prior.ID}
	if want == "" {
		gone = append(gone, res.Claim.ID)
	}
	var indexErr error
	receipt.Index, indexErr = rebuildLessonIndexFor(store.HomePath(), want, gone, now)
	if parsed.json {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(receipt); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
	} else {
		fmt.Fprintf(stdout, "lesson %s: %s -> %s\n  slot: %s::%s\n", res.Change, res.Prior.ID, res.Claim.ID, res.Claim.Entity, res.Claim.Predicate)
		if len(res.ClearedContest) > 0 {
			fmt.Fprintf(stdout, "  contest cleared: %s now stands alone\n", strings.Join(res.ClearedContest, ","))
		}
		if res.ContestedWith != "" {
			fmt.Fprintf(stdout, "  contested_with: %s (both kept; the next consumer that records a use supersedes the other)\n", res.ContestedWith)
		}
		fmt.Fprintf(stdout, "  undo: %s\n", receipt.Undo)
		if receipt.Index.Rebuilt {
			fmt.Fprintf(stdout, "  %s\n", receipt.Index.summary)
		}
		if receipt.Index.Indexed {
			fmt.Fprintf(stdout, "  indexed at rank %d of %d (a tool call serves the first 2 matching lessons in rank order)\n", receipt.Index.Rank, receipt.Index.Lessons)
		}
	}
	if indexErr == nil && receipt.Index.Warning != "" {
		fmt.Fprintln(stderr, "warning:", receipt.Index.Warning)
	}
	if indexErr != nil {
		fmt.Fprintf(stderr, "error: lesson %s is %s in the ledger (%s), but %v\n", res.Prior.ID, res.Change, res.Claim.ID, indexErr)
		return 1
	}
	return 0
}

// lessonChangeUndo renders the exact command that reverses one change.
func lessonChangeUndo(res mem.LessonChangeResult, actor string) string {
	by := " --by " + shellQuote(actor)
	switch res.Change {
	case mem.LessonChangeRetired:
		return "compound memory lesson restore " + res.Claim.ID + by
	case mem.LessonChangeRestored:
		return "compound memory lesson retire " + res.Claim.ID + " --reason '<why>'" + by
	}
	cmd := "compound memory lesson amend " + res.Claim.ID
	if res.Prior.Object != res.Claim.Object {
		cmd += " --how " + shellQuote(res.Prior.Object)
	}
	if prior, now := res.Prior.Qualifiers["trigger"], res.Claim.Qualifiers["trigger"]; prior != now {
		if prior == "" {
			cmd += " --no-trigger"
		} else {
			cmd += " --trigger " + shellQuote(prior)
		}
	}
	return cmd + by
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
