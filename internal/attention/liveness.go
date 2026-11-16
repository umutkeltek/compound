package attention

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"compound/internal/reviewartifact"
)

// Live count: an item counts under needs_you only when it is live. A review
// record that is already decided, already reviewed, the paperwork of a change
// that already ran, about something that no longer exists, test data, a file
// kept inside another record's folder, held on something else, or a repeat of
// another counted item with the same claim is SET ASIDE: it leaves the
// needs_you count and is listed in Report.SetAside with its reason, so nothing
// disappears silently and agents can see what to tidy.
//
// A record whose writer gave it only a template sentence is NOT set aside for
// that alone: whole kinds of real requests (job repairs, lock clearing, copy
// retirement) carry nothing else, so the rule would hide every future one. It
// stays counted and its group is marked needs_wording.
//
// This is a read-only projection. It never writes, resolves or closes a record;
// the record files stay exactly as the owners wrote them.
//
// Item-level checks run only when every record of a review group can be
// matched back to the queue's item list. A group that cannot be matched is kept
// whole and counted as before ("unchecked"): when in doubt, the projection
// shows the item rather than hides it, because a real decision hidden from the
// operator costs more than one extra line.

const (
	// BandSetAside names the not-yours band. It never appears in Report.Groups
	// (every existing reader treats each group there as surfaced); set-aside
	// entries live in Report.SetAside.
	BandSetAside = "set_aside"

	SetAsideAlreadyDecided   = "already_decided"
	SetAsideAlreadyReviewed  = "already_reviewed"
	SetAsideFinishedChange   = "finished_change"
	SetAsideRecordFolderFile = "record_folder_file"
	SetAsideTestData         = "test_data"
	SetAsideSubjectGone      = "subject_gone"
	SetAsideNoLongerCurrent  = "no_longer_current"
	SetAsideOnHold           = "on_hold"
	SetAsideDuplicate        = "duplicate"
)

// setAsideReasonOrder is the check order. The first reason that applies is the
// one recorded, so the most specific evidence wins over the catch-alls.
var setAsideReasonOrder = []string{
	SetAsideAlreadyDecided,
	SetAsideAlreadyReviewed,
	SetAsideFinishedChange,
	SetAsideRecordFolderFile,
	SetAsideTestData,
	SetAsideSubjectGone,
	SetAsideNoLongerCurrent,
	SetAsideOnHold,
	SetAsideDuplicate,
}

var setAsideReasonText = map[string]string{
	SetAsideAlreadyDecided:   "Already decided: the record is finished or a decision is recorded.",
	SetAsideAlreadyReviewed:  "Already reviewed: a review is recorded for this version of the record.",
	SetAsideFinishedChange:   "Paperwork of a change that already ran.",
	SetAsideRecordFolderFile: "A file kept inside another record's folder, not a decision.",
	SetAsideTestData:         "Test or demo data, not a real decision.",
	SetAsideSubjectGone:      "What it is about no longer exists.",
	SetAsideNoLongerCurrent:  "No longer matches the current state; it needs a fresh check first.",
	SetAsideOnHold:           "Held or waiting on something other than you.",
	SetAsideDuplicate:        "Asks the same thing as another item that is still counted.",
}

// SetAsideReasons returns the reason codes in check order.
func SetAsideReasons() []string {
	return append([]string{}, setAsideReasonOrder...)
}

// SetAsideReasonText returns the plain sentence for a reason code.
func SetAsideReasonText(reason string) string {
	return setAsideReasonText[reason]
}

// SetAside is one batch of records that asked to be counted under needs_you
// but are not the operator's: same source group, same reason.
type SetAside struct {
	Key             string   `json:"key"`
	Band            string   `json:"band"`
	Reason          string   `json:"reason"`
	ReasonText      string   `json:"reason_text"`
	Area            string   `json:"area"`
	Source          string   `json:"source"`
	Kind            string   `json:"kind"`
	Count           int      `json:"count"`
	OperatorSummary string   `json:"operator_summary"`
	Reversibility   string   `json:"reversibility"`
	RiskGrade       string   `json:"risk_grade"`
	SourceGroupKey  string   `json:"source_group_key"`
	DuplicateOf     string   `json:"duplicate_of,omitempty"`
	ItemIDs         []string `json:"item_ids"`
}

// Totals is the honest arithmetic behind the headline: every unit that asked
// to be counted under needs_you is either a live decision or set aside with a
// reason. LiveDecisions + SetAside == TotalRead, always.
type Totals struct {
	TotalRead        int            `json:"total_read"`
	LiveDecisions    int            `json:"live_decisions"`
	SetAside         int            `json:"set_aside"`
	SetAsideByReason map[string]int `json:"set_aside_by_reason"`
	// Unchecked counts live units the projection could not check record by
	// record (kept, never hidden).
	Unchecked int `json:"unchecked"`
}

// Probe is the read-only window the liveness checks use to look at the
// filesystem and the process table. Nil Inputs.Probe means the real machine.
type Probe interface {
	// PathState says whether a path exists. A path on a volume or root that
	// is itself missing is unknown, not gone.
	PathState(path string) SubjectState
	// ReadFile reads a file of at most limit bytes.
	ReadFile(path string, limit int64) ([]byte, error)
	// ProcessState says whether a process id is still running.
	ProcessState(pid int) SubjectState
	// FileTime is when a file was last written. Review records are written
	// once by their owners, so it stands in for a missing created_at.
	FileTime(path string) (time.Time, bool)
}

// SubjectState is the answer of a cheap, read-only existence check.
type SubjectState int

const (
	SubjectUnknown SubjectState = iota
	SubjectExists
	SubjectGone
)

type osProbe struct{}

func (osProbe) PathState(path string) SubjectState {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) {
		return SubjectUnknown
	}
	// Stat follows links: a link whose target was removed is a subject that
	// no longer exists.
	if _, err := os.Stat(path); err == nil {
		return SubjectExists
	} else if !errors.Is(err, fs.ErrNotExist) {
		return SubjectUnknown
	}
	// Gone only when a real parent directory is there to say so. If the first
	// existing ancestor is the root or the volumes folder, the disk that held
	// the path is missing (unmounted), which says nothing about the path.
	parent := filepath.Dir(filepath.Clean(path))
	for parent != "/" && parent != "." {
		if info, err := os.Stat(parent); err == nil {
			if !info.IsDir() || parent == "/Volumes" {
				return SubjectUnknown
			}
			return SubjectGone
		}
		parent = filepath.Dir(parent)
	}
	return SubjectUnknown
}

func (osProbe) ReadFile(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("%s is %d bytes, over the %d byte read limit", path, info.Size(), limit)
	}
	return io.ReadAll(io.LimitReader(f, limit+1))
}

func (osProbe) FileTime(path string) (time.Time, bool) {
	info, err := os.Stat(strings.TrimSpace(path))
	if err != nil {
		return time.Time{}, false
	}
	return info.ModTime(), true
}

func (osProbe) ProcessState(pid int) SubjectState {
	if pid <= 0 {
		return SubjectUnknown
	}
	err := syscall.Kill(pid, 0)
	switch {
	case err == nil, errors.Is(err, syscall.EPERM):
		return SubjectExists
	case errors.Is(err, syscall.ESRCH):
		return SubjectGone
	default:
		return SubjectUnknown
	}
}

const (
	recordReadLimit  = 4 << 20
	sidecarReadLimit = 256 << 10
)

// finishedMarkerSuffixes are record file stems that exist only once a change
// has run: the owner wrote its outcome. Their presence turns every other record
// of the same change into paperwork.
var finishedMarkerSuffixes = []string{".terminal", ".committed", ".applied", ".outcome", ".result"}

type reviewMember struct {
	item   reviewartifact.QueueItem
	group  int // index into liveness.groups
	reason string
	dupOf  string
}

type livenessGroup struct {
	source   reviewartifact.QueueGroup
	group    Group // projected group before the live check
	members  []int // indexes into liveness.members
	checked  bool
	postpone string
}

type liveness struct {
	probe    Probe
	now      time.Time
	report   reviewartifact.QueueReport
	groups   []livenessGroup
	members  []reviewMember
	areas    map[string]bool
	finished map[string]map[string]bool
	// dismissed holds, per area and change key, the latest time a recorded
	// dismissal closed one record of that change.
	dismissed map[string]map[string]time.Time
	records   map[string]map[string]any
	raw       map[string][]byte
}

// reviewLiveness splits the projected review groups into live groups and
// set-aside batches. projected[i] is the attention group built from
// report.Groups[i].
func reviewLiveness(report reviewartifact.QueueReport, projected []Group, probe Probe, now time.Time) (live []Group, setAside []SetAside, totalRead, unchecked int) {
	if probe == nil {
		probe = osProbe{}
	}
	l := &liveness{
		probe:     probe,
		now:       now,
		report:    report,
		areas:     map[string]bool{},
		finished:  map[string]map[string]bool{},
		dismissed: map[string]map[string]time.Time{},
		records:   map[string]map[string]any{},
		raw:       map[string][]byte{},
	}
	l.indexItems()
	for i := range projected {
		g := projected[i]
		if g.Band != BandNeedsYou || i >= len(report.Groups) {
			live = append(live, g)
			continue
		}
		lg := livenessGroup{source: report.Groups[i], group: g}
		members, ok := l.membersOf(report.Groups[i])
		// normalizeGroup gives a group with no count and no ids a count of
		// one; the unit read here must match what it will count live.
		unit := maxInt(g.Count, 1)
		totalRead += unit
		if !ok {
			unchecked += unit
			live = append(live, g)
			continue
		}
		lg.checked = true
		for _, item := range members {
			lg.members = append(lg.members, len(l.members))
			l.members = append(l.members, reviewMember{item: item, group: len(l.groups)})
		}
		l.groups = append(l.groups, lg)
	}
	for i := range l.members {
		l.members[i].reason = l.classify(l.members[i].item)
	}
	l.collapseDuplicates()
	for i := range l.groups {
		g, ok := l.liveGroup(i)
		if ok {
			live = append(live, g)
		}
	}
	setAside = l.setAsideBatches()
	return live, setAside, totalRead, unchecked
}

func (l *liveness) indexItems() {
	for _, item := range l.report.Items {
		area := strings.TrimSpace(item.Area)
		l.areas[area] = true
		if at, ok := closingDismissal(item); ok {
			if l.dismissed[area] == nil {
				l.dismissed[area] = map[string]time.Time{}
			}
			for _, key := range l.changeKeys(item) {
				if prev, seen := l.dismissed[area][key]; !seen || at.After(prev) {
					l.dismissed[area][key] = at
				}
			}
		}
		if !isFinishedMarker(item) {
			continue
		}
		if l.finished[area] == nil {
			l.finished[area] = map[string]bool{}
		}
		for _, key := range l.changeKeys(item) {
			l.finished[area][key] = true
		}
	}
}

// closingDismissal reports a recorded "dismissed" resolution and when it was
// made. Only a dismissal closes a change: "parked" postpones and "reviewed"
// records a look, so neither speaks for the change's other records.
func closingDismissal(item reviewartifact.QueueItem) (time.Time, bool) {
	r := item.Resolution
	if r == nil || strings.TrimSpace(r.Status) != "recorded" || strings.TrimSpace(r.Decision) != "dismissed" {
		return time.Time{}, false
	}
	return parseTime(r.ResolvedAt)
}

// changeDismissed reports a record of a change whose other record was
// dismissed AFTER this one was written: the dismissal was made with this
// record on file, so it closed it too. A record written after the dismissal,
// or one with no readable time, is new information and stays live. A record
// with no created_at is timed by its file, which its owner writes once; a
// rewrite after the dismissal makes it newer, which keeps it live.
func (l *liveness) changeDismissed(item reviewartifact.QueueItem) bool {
	marks := l.dismissed[strings.TrimSpace(item.Area)]
	if len(marks) == 0 {
		return false
	}
	created, ok := parseTime(item.CreatedAt)
	if !ok {
		created, ok = l.probe.FileTime(item.JSONPath)
	}
	if !ok {
		return false
	}
	for _, key := range l.changeKeys(item) {
		if at, seen := marks[key]; seen && !created.After(at) {
			return true
		}
	}
	return false
}

// membersOf returns the queue items that make up one review group, matched the
// way the review queue grouped them. It reports false when the match does not
// reproduce the group's own record count, so the caller keeps the group whole.
func (l *liveness) membersOf(group reviewartifact.QueueGroup) ([]reviewartifact.QueueItem, bool) {
	if len(l.report.Items) == 0 {
		return nil, false
	}
	var out []reviewartifact.QueueItem
	for _, item := range l.report.Items {
		if item.Area != group.Area || !itemInReviewGroup(item) || reviewGroupKey(item) != group.Key {
			continue
		}
		out = append(out, item)
	}
	want := maxInt(group.Count, len(group.ItemIDs))
	if len(out) == 0 || len(out) != want {
		return nil, false
	}
	return out, true
}

// itemInReviewGroup and reviewGroupKey mirror the review queue's own grouping
// (reviewartifact buildQueueGroups). membersOf refuses any group where the
// mirror does not reproduce the producer's count, so a drift here can only make
// a group unchecked, never hide it.
func itemInReviewGroup(item reviewartifact.QueueItem) bool {
	if item.Status == "" || item.Status == "unknown" || item.Status == "review_required" || item.Readiness == "needs_review" || item.Readiness == "ready_to_apply" || len(item.Actions) > 0 {
		return true
	}
	return !reviewartifact.TerminalStatus(item.Status)
}

func reviewGroupKey(item reviewartifact.QueueItem) string {
	if key := strings.TrimSpace(item.GroupKey); key != "" {
		return key
	}
	context := reviewartifact.FirstNonEmpty(item.ContextLabel, item.OperatorSummary, item.NextAction, item.ID, "review")
	return reviewartifact.FirstNonEmpty(item.Area, "review") + "|" + reviewartifact.Slugify(context, 72)
}

func (l *liveness) classify(item reviewartifact.QueueItem) string {
	switch {
	case reviewartifact.TerminalStatus(item.Status) || strings.TrimSpace(item.Band) == reviewartifact.BandFiled || item.Resolution != nil:
		return SetAsideAlreadyDecided
	case l.currentReviewedSidecar(item):
		return SetAsideAlreadyReviewed
	case l.changeFinished(item):
		return SetAsideFinishedChange
	case l.changeDismissed(item):
		// After finished_change: an outcome on file is stronger evidence
		// than a dismissal recorded on another record of the change.
		return SetAsideAlreadyDecided
	case l.insideRecordFolder(item.Area):
		return SetAsideRecordFolderFile
	case l.testData(item):
		return SetAsideTestData
	case l.subjectGone(item):
		return SetAsideSubjectGone
	case noLongerCurrent(item):
		return SetAsideNoLongerCurrent
	case strings.TrimSpace(item.Band) == reviewartifact.BandWatching:
		return SetAsideOnHold
	default:
		return ""
	}
}

// currentReviewedSidecar reports a `<stem>.reviewed.json` sidecar (live or
// archived) that reviewed THIS version of the record: its source hash matches
// the record's bytes, or, with no hash, it was written at or after the record.
// A review of an older version (the record was rewritten since) is stale and
// leaves the record live: a new promotion with fresh evidence must not hide
// behind last season's hold.
func (l *liveness) currentReviewedSidecar(item reviewartifact.QueueItem) bool {
	stem := recordStem(item.JSONPath)
	if stem == "" {
		return false
	}
	want := stem + ".reviewed.json"
	for _, path := range item.ExtraPaths {
		if filepath.Base(path) != want {
			continue
		}
		data, err := l.probe.ReadFile(path, sidecarReadLimit)
		if err != nil {
			continue
		}
		var sidecar map[string]any
		if json.Unmarshal(data, &sidecar) != nil {
			continue
		}
		if status := stringField(sidecar, "status"); status != "" && status != "recorded" {
			continue
		}
		if hash := stringField(sidecar, "source_hash"); strings.HasPrefix(hash, "sha256:") {
			raw, ok := l.recordBytes(item.JSONPath)
			if !ok {
				continue
			}
			sum := sha256.Sum256(raw)
			if strings.EqualFold(strings.TrimPrefix(hash, "sha256:"), hex.EncodeToString(sum[:])) {
				return true
			}
			continue
		}
		reviewedAt, okReviewed := parseTime(reviewartifact.FirstNonEmpty(stringField(sidecar, "created_at"), stringField(sidecar, "resolved_at")))
		createdAt, okCreated := parseTime(item.CreatedAt)
		if okReviewed && okCreated && !reviewedAt.Before(createdAt) {
			return true
		}
	}
	return false
}

func (l *liveness) changeFinished(item reviewartifact.QueueItem) bool {
	if isFinishedMarker(item) {
		return true
	}
	marks := l.finished[strings.TrimSpace(item.Area)]
	for _, key := range l.changeKeys(item) {
		if marks[key] {
			return true
		}
	}
	return false
}

// insideRecordFolder reports an area nested under another area that holds
// records of its own (e.g. `<area>/<record>.preimage`): its files are the
// inside of one record, not separate decisions.
func (l *liveness) insideRecordFolder(area string) bool {
	parts := strings.Split(strings.Trim(filepath.ToSlash(strings.TrimSpace(area)), "/"), "/")
	for i := 1; i < len(parts); i++ {
		if parent := strings.Join(parts[:i], "/"); parent != "" && l.areas[parent] {
			return true
		}
	}
	return false
}

// testData recognizes fixture and demo records that reached the real store:
// a fixture provider, a fixture decision id, or a demo suite built from a dev
// build. The markers are narrow on purpose so a product record that merely
// mentions a demo is never hidden.
func (l *liveness) testData(item reviewartifact.QueueItem) bool {
	if fixtureToken(item.ID) {
		return true
	}
	record, ok := l.record(item.JSONPath)
	if !ok {
		return false
	}
	if fixtureToken(stringField(record, "id")) || fixtureToken(stringField(record, "decision_id")) {
		return true
	}
	suite := strings.ToLower(stringField(record, "suite"))
	if (suite == "demo" || suite == "fixture" || suite == "example") && strings.EqualFold(stringField(record, "build_sha"), "dev") {
		return true
	}
	for _, value := range providerValues(record) {
		if strings.HasPrefix(strings.ToLower(value), "fixture") {
			return true
		}
	}
	return false
}

// subjectGone checks the subjects a record names in a known field, cheaply and
// read-only: a worktree path, a git root, the process of a lock waiter, or the
// lock file a stale-lock plan was written to clear.
func (l *liveness) subjectGone(item reviewartifact.QueueItem) bool {
	record, ok := l.record(item.JSONPath)
	if !ok {
		return false
	}
	if worktree, ok := record["worktree"].(map[string]any); ok {
		if path := stringField(worktree, "path"); path != "" && l.probe.PathState(path) == SubjectGone {
			return true
		}
	}
	if path := stringField(record, "git_root"); path != "" && l.probe.PathState(path) == SubjectGone {
		return true
	}
	if pid, ok := intField(record, "waiter_pid"); ok && pid > 0 && l.probe.ProcessState(pid) == SubjectGone {
		return true
	}
	if path := lockFileOfPlan(record); path != "" && l.probe.PathState(path) == SubjectGone {
		return true
	}
	return false
}

// lockFileOfPlan names the lock file a stale-lock recovery plan was written to
// clear: the plan snapshots the lock it found in a primary checkout
// (state.snapshot.repo plus a lock identity), so the file is
// <repo>/.git/index.lock. A lock that is still there keeps the plan live, even
// if it is a newer lock: the projection never decides that one away.
func lockFileOfPlan(record map[string]any) string {
	state, _ := record["state"].(map[string]any)
	snapshot, _ := state["snapshot"].(map[string]any)
	repo := stringField(snapshot, "repo")
	if _, hasLock := snapshot["lock"].(map[string]any); !hasLock || repo == "" || !filepath.IsAbs(repo) {
		return ""
	}
	return filepath.Join(repo, ".git", "index.lock")
}

// noQuestion trusts only the queue's own "fallback" mark: no writer or
// annotator could say what the record asks. It never sets a record aside; it
// marks the live group needs_wording so an agent words it.
func noQuestion(item reviewartifact.QueueItem) bool {
	return strings.TrimSpace(item.SummarySource) == reviewartifact.SummarySourceFallback
}

// noLongerCurrent reads the source-control owner's own verdict on a dirty-plan
// whose repository state moved on (internal/authority
// annotateSourceControlCleanupItem). The owner sets no structured flag for it,
// so the prefixes are matched; if the wording changes the record simply stays
// live, which is the safe direction.
func noLongerCurrent(item reviewartifact.QueueItem) bool {
	reason := strings.TrimSpace(item.BlockedReason)
	for _, prefix := range []string{
		"current source-control state changed since this dirty-plan",
		"source-control cleanup plan invalid",
	} {
		if strings.HasPrefix(reason, prefix) {
			return true
		}
	}
	return false
}

// postponedUntil returns the latest owner deferral date that has already
// passed: the record came back because its date arrived, not because it is new.
func (l *liveness) postponedUntil(item reviewartifact.QueueItem) string {
	latest := ""
	var latestAt time.Time
	for _, path := range item.ExtraPaths {
		if !strings.HasSuffix(path, ".decision.json") {
			continue
		}
		data, err := l.probe.ReadFile(path, sidecarReadLimit)
		if err != nil {
			continue
		}
		var decision map[string]any
		if json.Unmarshal(data, &decision) != nil || stringField(decision, "decision") != "defer" {
			continue
		}
		until := stringField(decision, "defer_until")
		at, ok := parseTime(until)
		if !ok || at.After(l.now) {
			continue
		}
		if latest == "" || at.After(latestAt) {
			latest, latestAt = at.UTC().Format(time.RFC3339), at
		}
	}
	return latest
}

// collapseDuplicates keeps one live representative per real-world question.
// Two records are the same item when they share an id in one area (several
// files of one record), or carry the same claim in one area (the same ask
// filed again). The claim is the writer's own statement of what the record
// asks (decision_card.claim). A shared summary sentence is not enough: owners
// stamp one template sentence on many different proposals, and folding those
// would hide every proposal but one. The oldest group and record win, so the
// representative is stable from one read to the next.
func (l *liveness) collapseDuplicates() {
	order := make([]int, 0, len(l.groups))
	for i := range l.groups {
		order = append(order, i)
	}
	sort.SliceStable(order, func(a, b int) bool {
		ga, gb := l.groups[order[a]].group, l.groups[order[b]].group
		ta, okA := parseTime(ga.OldestCreatedAt)
		tb, okB := parseTime(gb.OldestCreatedAt)
		if okA && okB && !ta.Equal(tb) {
			return ta.Before(tb)
		}
		if okA != okB {
			return okA
		}
		return ga.Key < gb.Key
	})
	byID := map[string]string{}
	byQuestion := map[string]string{}
	for _, gi := range order {
		members := append([]int{}, l.groups[gi].members...)
		sort.SliceStable(members, func(a, b int) bool {
			ia, ib := l.members[members[a]].item, l.members[members[b]].item
			ta, okA := parseTime(ia.CreatedAt)
			tb, okB := parseTime(ib.CreatedAt)
			if okA && okB && !ta.Equal(tb) {
				return ta.Before(tb)
			}
			if okA != okB {
				return okA
			}
			return ia.JSONPath < ib.JSONPath
		})
		for _, mi := range members {
			m := &l.members[mi]
			if m.reason != "" {
				continue
			}
			key := l.groups[gi].group.Key
			idKey := strings.TrimSpace(m.item.Area) + "\x00" + strings.TrimSpace(m.item.ID)
			questionKey := ""
			if m.item.DecisionCard != nil {
				if claim := normalizeQuestion(m.item.DecisionCard.Claim); claim != "" {
					questionKey = strings.TrimSpace(m.item.Area) + "\x00" + claim + "\x00" + normalizeQuestion(m.item.ContextLabel)
				}
			}
			if rep, ok := byID[idKey]; ok {
				m.reason, m.dupOf = SetAsideDuplicate, rep
				continue
			}
			if rep, ok := byQuestion[questionKey]; ok && questionKey != "" {
				m.reason, m.dupOf = SetAsideDuplicate, rep
				continue
			}
			byID[idKey] = key
			if questionKey != "" {
				byQuestion[questionKey] = key
			}
		}
	}
}

func (l *liveness) liveGroup(gi int) (Group, bool) {
	lg := l.groups[gi]
	g := lg.group
	var ids []string
	oldest := ""
	postponed := ""
	needsWording := false
	setAsideAny := false
	for _, mi := range lg.members {
		m := l.members[mi]
		if m.reason != "" {
			setAsideAny = true
			continue
		}
		ids = append(ids, m.item.ID)
		needsWording = needsWording || noQuestion(m.item)
		oldest = olderTimestamp(oldest, m.item.CreatedAt)
		if until := l.postponedUntil(m.item); until > postponed {
			postponed = until
		}
	}
	if len(ids) == 0 {
		return Group{}, false
	}
	records := 0
	for _, m := range l.members {
		if m.reason == "" && m.group == gi {
			records++
		}
		if m.reason == SetAsideDuplicate && m.dupOf == g.Key {
			records++
			oldest = olderTimestamp(oldest, m.item.CreatedAt)
			if until := l.postponedUntil(m.item); until > postponed {
				postponed = until
			}
		}
	}
	g.Count = len(ids)
	g.RecordCount = records
	g.ItemIDs = uniqueStrings(ids)
	g.OldestCreatedAt = firstNonEmpty(oldest, g.OldestCreatedAt)
	g.PostponedUntil = postponed
	g.NeedsWording = needsWording
	if setAsideAny && g.Action != nil {
		// A batch action built for the whole group would also reach the
		// records set aside; leave it visible but never runnable.
		action := *g.Action
		action.Kind = ActionCopyOnly
		action.RequiresConfirmation = true
		g.Action = &action
	}
	return g, true
}

func (l *liveness) setAsideBatches() []SetAside {
	type batchKey struct {
		group  string
		reason string
	}
	batches := map[batchKey]*SetAside{}
	var keys []batchKey
	byKey := map[string]Group{}
	for _, lg := range l.groups {
		byKey[lg.group.Key] = lg.group
	}
	for _, m := range l.members {
		if m.reason == "" {
			continue
		}
		// Repeats are batched under the item they repeat (one line for the
		// 30 copies of one question), everything else under its own group.
		source := l.groups[m.group].group
		if m.reason == SetAsideDuplicate {
			if rep, ok := byKey[m.dupOf]; ok {
				source = rep
			}
		}
		k := batchKey{group: source.Key, reason: m.reason}
		b := batches[k]
		if b == nil {
			b = &SetAside{
				Key:             source.Key + "#" + m.reason,
				Band:            BandSetAside,
				Reason:          m.reason,
				ReasonText:      setAsideReasonText[m.reason],
				Area:            source.Area,
				Source:          source.Source,
				OperatorSummary: source.OperatorSummary,
				SourceGroupKey:  source.Key,
				DuplicateOf:     m.dupOf,
			}
			batches[k] = b
			keys = append(keys, k)
		}
		b.Count++
		b.ItemIDs = append(b.ItemIDs, m.item.ID)
	}
	out := make([]SetAside, 0, len(keys))
	for _, k := range keys {
		b := batches[k]
		b.ItemIDs = uniqueStrings(b.ItemIDs)
		out = append(out, *b)
	}
	return out
}

func sortSetAside(entries []SetAside) {
	rank := map[string]int{}
	for i, reason := range setAsideReasonOrder {
		rank[reason] = i
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if rank[entries[i].Reason] != rank[entries[j].Reason] {
			return rank[entries[i].Reason] < rank[entries[j].Reason]
		}
		if entries[i].Area != entries[j].Area {
			return entries[i].Area < entries[j].Area
		}
		return entries[i].Key < entries[j].Key
	})
}

func (l *liveness) recordBytes(path string) ([]byte, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, false
	}
	if raw, ok := l.raw[path]; ok {
		return raw, raw != nil
	}
	raw, err := l.probe.ReadFile(path, recordReadLimit)
	if err != nil {
		l.raw[path] = nil
		return nil, false
	}
	l.raw[path] = raw
	return raw, true
}

func (l *liveness) record(path string) (map[string]any, bool) {
	path = strings.TrimSpace(path)
	if record, ok := l.records[path]; ok {
		return record, record != nil
	}
	raw, ok := l.recordBytes(path)
	var record map[string]any
	if ok && json.Unmarshal(raw, &record) != nil {
		record = nil
	}
	l.records[path] = record
	return record, record != nil
}

func isFinishedMarker(item reviewartifact.QueueItem) bool {
	stem := recordStem(item.JSONPath)
	for _, suffix := range finishedMarkerSuffixes {
		if strings.HasSuffix(stem, suffix) {
			return true
		}
	}
	return false
}

// changeKeys names the change a record belongs to: the leading token of its id
// and of its file name, and the plan a review record names in its plan_id (or
// request.plan_id) field. Owners write a change's records under one plan id, but
// some name the file after the action and the id after the plan, or the
// reverse, so all are kept.
func (l *liveness) changeKeys(item reviewartifact.QueueItem) []string {
	values := []string{item.ID, recordStem(item.JSONPath)}
	if record, ok := l.record(item.JSONPath); ok {
		values = append(values, stringField(record, "plan_id"))
		if request, ok := record["request"].(map[string]any); ok {
			values = append(values, stringField(request, "plan_id"))
		}
	}
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if i := strings.Index(value, "."); i >= 0 {
			value = value[:i]
		}
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func recordStem(path string) string {
	base := filepath.Base(strings.TrimSpace(path))
	if base == "." || base == "/" || base == "" {
		return ""
	}
	return strings.TrimSuffix(base, ".json")
}

func fixtureToken(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return false
	}
	for _, token := range strings.FieldsFunc(value, func(r rune) bool { return r == '_' || r == '-' || r == '.' || r == ':' || r == '/' }) {
		if token == "fixture" {
			return true
		}
	}
	return false
}

func providerValues(record map[string]any) []string {
	var out []string
	if value := stringField(record, "provider"); value != "" {
		out = append(out, value)
	}
	for _, value := range record {
		if nested, ok := value.(map[string]any); ok {
			if provider := stringField(nested, "provider"); provider != "" {
				out = append(out, provider)
			}
		}
	}
	return out
}

func normalizeQuestion(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(value)), " ")
}

func stringField(record map[string]any, key string) string {
	if record == nil {
		return ""
	}
	value, _ := record[key].(string)
	return strings.TrimSpace(value)
}

func intField(record map[string]any, key string) (int, bool) {
	if record == nil {
		return 0, false
	}
	switch value := record[key].(type) {
	case float64:
		return int(value), true
	case json.Number:
		n, err := value.Int64()
		return int(n), err == nil
	default:
		return 0, false
	}
}
