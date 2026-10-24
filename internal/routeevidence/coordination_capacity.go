package routeevidence

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"compound/internal/work"
)

// Coordination capacity observation.
//
// Coordination projects the planning half of `work continue` for every actor in
// ONE request. The legacy path asked the global lane-capacity guard once per
// eligible actor, and the guard re-read every capacity source per call: the
// operator lane-alias file, both usage JSONL feeds, the switchback account
// projection, and — for codex lanes — the multi-auth quota cache. N eligible
// actors therefore read the same bytes N times, and the account projection was
// re-parsed N times.
//
// This observation keeps the SAME decisions — alias precedence, provider-family
// matching, freshness, future timestamps, expected duration, resets,
// provenance, confirmation and alternative ladders, malformed-row skipping
// versus scanner errors, absent versus unreadable — but reads each APPLICABLE
// source at most once per request and evaluates every actor separately from
// the cached facts. It never caches an actor verdict and never mutates a shared
// row slice: each actor filters its own copy of the parsed rows.
//
// That once-per-request bound covers the COORDINATION path. A declared-source
// request is delegated to the EXISTING fresh guard, which reads its own sources
// and is not attributed to this observation's counters; a request that mixes a
// declared-source actor with a coordination actor therefore reads the usage
// feeds twice. The first slice delegates rather than re-implementing the
// declared-source semantics, and no coordination request carries a declared
// source today.
//
// The observation is READ-ONLY by construction. It never records
// capacity-refusal evidence, never runs a live provider probe on the
// coordination path, and never touches the global work.LaneCapacityGuard. It
// carries no execution authority: it answers "what does the capacity source
// say", not "may this actor run".
//
// SCOPE: one observation serves one coordination request. Callers build ONE
// observation per request (cmd/compound does exactly that) and hand it to
// work.CoordinationOptions.CapacityGuard.

// coordinationCapacitySessionLimit bounds how many distinct request instants one
// observation remembers. An observation is request-scoped, so this is a safety
// net against unbounded growth, not a cache policy.
const coordinationCapacitySessionLimit = 8

// CoordinationSourceAccess counts how an observation touched each capacity source.
type CoordinationSourceAccess struct {
	// Reads counts content reads per source path.
	Reads map[string]int
	// Stats counts existence probes per source path.
	Stats map[string]int
}

// CoordinationCapacityObservation is a request-scoped lane-capacity observation. It answers the
// same questions as the production guard, but reads each applicable source once per request.
type CoordinationCapacityObservation struct {
	cfg   LaneCapacityConfig
	probe codexBarCapacityProbeFunc

	mu       sync.Mutex
	sessions map[string]*coordinationCapacitySession
	reads    map[string]int
	stats    map[string]int
}

// NewCoordinationCapacityObservation builds a request-scoped capacity observation.
func NewCoordinationCapacityObservation(cfg LaneCapacityConfig) *CoordinationCapacityObservation {
	return &CoordinationCapacityObservation{
		cfg:      cfg,
		probe:    ProbeCodexBarCapacity,
		sessions: map[string]*coordinationCapacitySession{},
		reads:    map[string]int{},
		stats:    map[string]int{},
	}
}

// CoordinationLaneCapacityObservation is the coordination-only observation constructor. It
// returns the EXISTING work.LaneCapacityGuardFunc seam, so a caller can inject it through
// work.CoordinationOptions without a new package, a new global, or a new execution authority.
//
// The returned guard is request-scoped: build ONE per coordination request.
func CoordinationLaneCapacityObservation(cfg LaneCapacityConfig) work.LaneCapacityGuardFunc {
	return NewCoordinationCapacityObservation(cfg).Guard()
}

// Guard returns the request-scoped guard function.
func (o *CoordinationCapacityObservation) Guard() work.LaneCapacityGuardFunc {
	return func(req work.LaneCapacityGuardRequest) (work.LaneCapacityGuardResult, error) {
		return o.observe(req)
	}
}

// SourceAccess reports how many times this observation read or stat'ed each capacity source
// on the COORDINATION path. It exists so the read-once property is observable rather than
// asserted. A declared-source request is delegated to the existing fresh guard, whose own
// reads are not attributed here.
func (o *CoordinationCapacityObservation) SourceAccess() CoordinationSourceAccess {
	o.mu.Lock()
	defer o.mu.Unlock()
	reads := make(map[string]int, len(o.reads))
	for path, count := range o.reads {
		reads[path] = count
	}
	stats := make(map[string]int, len(o.stats))
	for path, count := range o.stats {
		stats[path] = count
	}
	return CoordinationSourceAccess{Reads: reads, Stats: stats}
}

func (o *CoordinationCapacityObservation) countRead(path string) {
	if strings.TrimSpace(path) == "" {
		return
	}
	o.reads[path]++
}

func (o *CoordinationCapacityObservation) countStat(path string) {
	if strings.TrimSpace(path) == "" {
		return
	}
	o.stats[path]++
}

// observe answers one actor's capacity question from the request's cached source facts.
func (o *CoordinationCapacityObservation) observe(req work.LaneCapacityGuardRequest) (work.LaneCapacityGuardResult, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	// A tuple-backed request with no declared source consults no Compound capacity
	// source at all: runtime quota stays execution-gateway-owned.
	if req.TupleBacked && req.CapacitySource == nil {
		return work.LaneCapacityGuardResult{
			Capacity: work.DispatchLaneCapacity{State: "gateway_owned"},
			Message:  "no Compound capacity source declared; runtime quota remains execution-gateway-owned",
		}, nil
	}
	session := o.session(req.HomePath, req.Now)
	if req.CapacitySource != nil {
		// A declared source keeps the EXISTING fresh guard semantics, including its
		// live-probe fallback. The observation only adds the read-only posture.
		decision, err := session.guardDeclared(req, o.probe)
		if err != nil {
			return work.LaneCapacityGuardResult{}, err
		}
		return laneCapacityGuardResult(decision), nil
	}
	decision, err := session.guardLegacy(req)
	if err != nil {
		return work.LaneCapacityGuardResult{}, err
	}
	return laneCapacityGuardResult(decision), nil
}

// session returns the request-scoped fact cache for one home and one instant.
func (o *CoordinationCapacityObservation) session(home string, now time.Time) *coordinationCapacitySession {
	key := home + "\x00" + now.UTC().Format(time.RFC3339Nano)
	if existing, ok := o.sessions[key]; ok {
		return existing
	}
	if len(o.sessions) >= coordinationCapacitySessionLimit {
		o.sessions = map[string]*coordinationCapacitySession{}
	}
	session := &coordinationCapacitySession{
		home: home,
		cfg:  normalizeLaneCapacityConfig(o.cfg, home),
		o:    o,
	}
	o.sessions[key] = session
	return session
}

// coordinationCapacitySession holds the lazily-read, request-scoped capacity facts. Every cached
// fact is INDEPENDENT of the actor and of the request instant: the freshness and family tests run
// per actor against these same parsed bytes.
type coordinationCapacitySession struct {
	home string
	cfg  LaneCapacityConfig
	o    *CoordinationCapacityObservation

	aliases     map[string]string
	aliasesErr  error
	aliasesRead bool

	usageRowsCache []codexBarCapacityRow
	usageInstalled bool
	usageErr       error
	usageRead      bool

	quotaModTime  time.Time
	quotaPresent  bool
	quotaStatErr  error
	quotaStatRead bool

	quotaCache   quotaCacheDocument
	quotaReadErr error
	quotaRead    bool

	account     *AccountProjection
	accountErr  error
	accountRead bool

	manifest     ModelCapabilitiesManifest
	manifestErr  error
	manifestRead bool

	installed     bool
	installedRead bool
}

// guardLegacy evaluates the legacy (undeclared) capacity path for one actor.
func (s *coordinationCapacitySession) guardLegacy(req work.LaneCapacityGuardRequest) (LaneCapacityDecision, error) {
	expected := s.expectedMinutes(req.ExpectedMinutes)
	capacity, err := s.readCapacity(req.Lane, req.Now, expected)
	if err != nil {
		return LaneCapacityDecision{}, err
	}
	alternatives := s.capacityAlternativeLadder(s.home, req.CapabilityClass, req.Alternatives)
	return decideLaneCapacity(req.HomePath, req.Lane, req.Now, req.Confirm, alternatives, s.decisionConfig(expected), capacity)
}

// guardDeclared delegates a declared-source request to the EXISTING fresh guard path, with the
// observation's read-only posture: refusal evidence is never recorded from here. The delegated
// path reads its own sources, so those reads are not attributed to this observation's counters
// and are not shared with the request's cached facts.
func (s *coordinationCapacitySession) guardDeclared(req work.LaneCapacityGuardRequest, probe codexBarCapacityProbeFunc) (LaneCapacityDecision, error) {
	if probe == nil {
		probe = ProbeCodexBarCapacity
	}
	effective := s.cfg
	effective.ExpectedMinutes = req.ExpectedMinutes
	effective.SuppressRefusalEvidence = true
	return guardDeclaredLaneCapacityWithConfigAndProbe(
		req.HomePath,
		req.Lane,
		req.CapabilityClass,
		LaneCapacitySourceRef{
			Kind:             req.CapacitySource.Kind,
			FreshnessMinutes: req.CapacitySource.FreshnessMinutes,
			Ref:              req.CapacitySource.Ref,
		},
		req.Now,
		req.ExpectedMinutes,
		req.Confirm,
		req.Alternatives,
		true,
		effective,
		probe,
	)
}

// expectedMinutes mirrors the production guard: the request's expected duration wins, and an
// unset request falls back to the default window.
func (s *coordinationCapacitySession) expectedMinutes(requested int) int {
	if requested > 0 {
		return requested
	}
	return defaultExpectedMinutes
}

// decisionConfig is the config the decision runs under. The observation is read-only by
// construction, so refusal evidence is always suppressed.
func (s *coordinationCapacitySession) decisionConfig(expected int) LaneCapacityConfig {
	cfg := s.cfg
	cfg.ExpectedMinutes = expected
	cfg.SuppressRefusalEvidence = true
	return cfg
}

// readCapacity reproduces ReadLaneCapacityWithConfig over the request's cached source facts.
func (s *coordinationCapacitySession) readCapacity(lane string, now time.Time, expected int) (LaneCapacity, error) {
	key, aliasNote := s.resolveLaneCapacityKey(lane)
	installed := s.sourceInstalled()
	capacity := LaneCapacity{State: CapacityUnknown, DataRef: "no matching capacity row", SourceInstalled: installed}
	if !installed {
		capacity.DataRef = "no capacity source installed"
	}
	if key == "" {
		if aliasNote != "" {
			capacity.DataRef = capacity.DataRef + "; " + aliasNote
		}
		return capacity, nil
	}

	usage, ok, err := s.readCodexBarCapacity(key, now, expected)
	if err != nil {
		return LaneCapacity{}, err
	}
	if ok {
		capacity = usage
	} else if usage.DataRef != "" {
		// Annotated unknown: carry the annotation forward so callers can tell WHY
		// the lane is unknown — zero rows vs only-stale rows.
		capacity.DataRef = usage.DataRef
		if !usage.LastProven.IsZero() {
			capacity.LastProven = usage.LastProven
		}
		if usage.Source != "" {
			capacity.Source = usage.Source
		}
	}

	wall, ok, err := s.readFreshQuotaWall(key, now)
	if err != nil {
		return LaneCapacity{}, err
	}
	if ok {
		wall.Remaining = capacity.Remaining
		wall.ResetsAt = capacity.ResetsAt
		capacity = wall
	}

	dead, ok, err := s.readDeadAccountProjection(key, now)
	if err != nil {
		return LaneCapacity{}, err
	}
	if ok {
		dead.Remaining = capacity.Remaining
		dead.ResetsAt = capacity.ResetsAt
		capacity = dead
	}
	capacity.SourceInstalled = installed
	// Compose malformed-alias note onto any final-unknown state.
	if capacity.State == CapacityUnknown && aliasNote != "" && !strings.Contains(capacity.DataRef, aliasNote) {
		capacity.DataRef = capacity.DataRef + "; " + aliasNote
	}
	return capacity, nil
}

// readCodexBarCapacity evaluates one family against the parsed usage rows.
func (s *coordinationCapacitySession) readCodexBarCapacity(key string, now time.Time, expected int) (LaneCapacity, bool, error) {
	rows, installed, err := s.usageRows()
	if err != nil {
		return LaneCapacity{}, false, err
	}
	matches := func(provider string) bool { return normalizeLaneCapacityKey(provider) == key }
	// filterCapacityRows copies: the shared parsed rows are never sorted or mutated
	// by one actor's decision.
	filtered := filterCapacityRows(rows, now, matches)
	return decideCodexBarCapacity(filtered, installed, key, now, expected, capacityFreshness(key))
}

// usageRows parses every configured usage feed at most once per request.
func (s *coordinationCapacitySession) usageRows() ([]codexBarCapacityRow, bool, error) {
	if !s.usageRead {
		s.usageRead = true
		var rows []codexBarCapacityRow
		installed := false
		for _, src := range capacityRowSourcesFor(s.cfg.Paths) {
			fileRows, fileInstalled, err := s.readUsageSource(src)
			if err != nil {
				s.usageErr = err
				s.usageRowsCache = nil
				s.usageInstalled = false
				return nil, false, err
			}
			installed = installed || fileInstalled
			rows = append(rows, fileRows...)
		}
		s.usageRowsCache = rows
		s.usageInstalled = installed
	}
	return s.usageRowsCache, s.usageInstalled, s.usageErr
}

func (s *coordinationCapacitySession) readUsageSource(src capacityRowSource) ([]codexBarCapacityRow, bool, error) {
	if strings.TrimSpace(src.path) == "" {
		return nil, false, nil
	}
	s.o.countRead(src.path)
	return readCapacityRowsFromSourceFile(src)
}

// readFreshQuotaWall evaluates the multi-auth quota cache for the codex family only. The cache
// is stat'ed once and read at most once per request.
func (s *coordinationCapacitySession) readFreshQuotaWall(key string, now time.Time) (LaneCapacity, bool, error) {
	if key != "codex" || strings.TrimSpace(s.cfg.Paths.QuotaCachePath) == "" {
		return LaneCapacity{}, false, nil
	}
	observed, present, err := s.quotaCacheModTime()
	if err != nil {
		return LaneCapacity{}, false, err
	}
	if !present {
		return LaneCapacity{}, false, nil
	}
	age := now.Sub(observed)
	if age < -capacityClockSkew || age > capacityWallFreshness {
		return LaneCapacity{}, false, nil
	}
	cache, err := s.quotaCacheContent()
	if err != nil {
		return LaneCapacity{}, false, err
	}
	return decideFreshQuotaWall(observed, cache)
}

func (s *coordinationCapacitySession) quotaCacheModTime() (time.Time, bool, error) {
	if !s.quotaStatRead {
		s.quotaStatRead = true
		path := s.cfg.Paths.QuotaCachePath
		s.o.countStat(path)
		info, err := os.Stat(path)
		switch {
		case err == nil:
			s.quotaPresent = true
			s.quotaModTime = info.ModTime()
		case os.IsNotExist(err):
			s.quotaPresent = false
		default:
			s.quotaStatErr = fmt.Errorf("stat quota cache: %w", err)
		}
	}
	return s.quotaModTime, s.quotaPresent, s.quotaStatErr
}

func (s *coordinationCapacitySession) quotaCacheContent() (quotaCacheDocument, error) {
	if !s.quotaRead {
		s.quotaRead = true
		path := s.cfg.Paths.QuotaCachePath
		s.o.countRead(path)
		data, err := os.ReadFile(path)
		switch {
		case err != nil:
			s.quotaReadErr = fmt.Errorf("read quota cache: %w", err)
		default:
			var cache quotaCacheDocument
			if err := json.Unmarshal(data, &cache); err != nil {
				s.quotaReadErr = fmt.Errorf("parse quota cache: %w", err)
			} else {
				s.quotaCache = cache
			}
		}
	}
	return s.quotaCache, s.quotaReadErr
}

// readDeadAccountProjection evaluates the switchback account projection for one provider.
func (s *coordinationCapacitySession) readDeadAccountProjection(key string, now time.Time) (LaneCapacity, bool, error) {
	if strings.TrimSpace(s.cfg.Paths.AccountProjectionPath) == "" {
		return LaneCapacity{}, false, nil
	}
	projection, err := s.accountProjection()
	if err != nil {
		return LaneCapacity{}, false, err
	}
	if projection == nil {
		return LaneCapacity{}, false, nil
	}
	return decideDeadAccountProjection(projection, key, now)
}

func (s *coordinationCapacitySession) accountProjection() (*AccountProjection, error) {
	if !s.accountRead {
		s.accountRead = true
		path := s.cfg.Paths.AccountProjectionPath
		s.o.countRead(path)
		projection, err := readAccountProjectionFile(path)
		if err != nil {
			s.accountErr = err
		} else {
			s.account = projection
		}
	}
	return s.account, s.accountErr
}

// sourceInstalled reports whether ANY configured capacity source file exists. The existence
// probes happen once per request, not once per actor.
func (s *coordinationCapacitySession) sourceInstalled() bool {
	if !s.installedRead {
		s.installedRead = true
		s.installed = capacitySourceInstalledCounted(s.cfg.Paths, s.o.countStat)
	}
	return s.installed
}

// resolveLaneCapacityKey layers the operator-owned alias override on top of the pure structural
// rules, reading the alias file at most once per request. A malformed alias file is NOT a hard
// error: it is reported as a note that composes onto a final-unknown DataRef, exactly as the
// production path does.
func (s *coordinationCapacitySession) resolveLaneCapacityKey(lane string) (string, string) {
	trimmed := strings.ToLower(strings.TrimSpace(lane))
	if key := exactLaneCapacityFamily(trimmed); key != "" {
		return s.aliasObservation(key)
	}
	base := trimmed
	if idx := strings.Index(base, ":"); idx >= 0 {
		base = base[:idx]
	}
	if key := exactLaneCapacityFamily(base); key != "" {
		return s.aliasObservation(key)
	}
	aliases, err := s.laneAliases()
	if err == nil {
		if family, ok := aliases[trimmed]; ok {
			return family, ""
		}
		if family, ok := aliases[base]; ok {
			return family, ""
		}
	}
	inferred := inferLaneCapacityFamily(base)
	if err != nil {
		return inferred, "lane-aliases.json unreadable"
	}
	return inferred, ""
}

// aliasObservation loads the alias file (if any) purely to observe whether it is malformed.
func (s *coordinationCapacitySession) aliasObservation(key string) (string, string) {
	if _, err := s.laneAliases(); err != nil {
		return key, "lane-aliases.json unreadable"
	}
	return key, ""
}

func (s *coordinationCapacitySession) laneAliases() (map[string]string, error) {
	if !s.aliasesRead {
		s.aliasesRead = true
		path := s.cfg.Paths.AliasesPath
		if strings.TrimSpace(path) != "" {
			s.o.countRead(path)
		}
		s.aliases, s.aliasesErr = loadLaneCapacityAliases(path)
	}
	return s.aliases, s.aliasesErr
}

// capacityAlternativeLadder keeps the production ladder semantics: an explicit ladder wins, an
// empty capability class has no ladder, and a model-capabilities read that fails yields no
// ladder rather than an error.
func (s *coordinationCapacitySession) capacityAlternativeLadder(homePath, capabilityClass string, provided []string) []string {
	if len(provided) > 0 {
		return append([]string(nil), provided...)
	}
	class := strings.TrimSpace(capabilityClass)
	if class == "" {
		return nil
	}
	manifest, err := s.modelCapabilities(homePath)
	if err != nil {
		return nil
	}
	return append([]string(nil), manifest.RoutingDefaults[class]...)
}

func (s *coordinationCapacitySession) modelCapabilities(homePath string) (ModelCapabilitiesManifest, error) {
	if !s.manifestRead {
		s.manifestRead = true
		s.o.countRead(ModelCapabilitiesPath(homePath))
		manifest, _, _, err := loadModelCapabilitiesSnapshot(homePath)
		if err != nil {
			s.manifestErr = err
		} else {
			s.manifest = manifest
		}
	}
	return s.manifest, s.manifestErr
}
