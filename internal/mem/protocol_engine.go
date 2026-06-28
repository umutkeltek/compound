package mem

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"sync"
)

// Transport carries one protocol round-trip. The wire is the swap seam: a held coprocess (warm
// index, the solo default), a per-op exec (simplest), or a socket are interchangeable impls
// behind identical marshalling. compound owns the protocol; the engine is rented behind it.
type Transport interface {
	RoundTrip(req Request) (Response, error)
	Close() error
}

// HeldCoprocessTransport spawns the engine binary ONCE and holds framed JSON over its stdio, so
// the RAM-resident index stays warm across queries (no reload per call). Serialized by a mutex
// (single-writer through the held pipe). stderr is the engine's log channel; stdout is data only.
type HeldCoprocessTransport struct {
	argv []string
	env  []string

	mu      sync.Mutex
	cmd     *exec.Cmd
	enc     *json.Encoder
	dec     *json.Decoder
	started bool
}

func NewHeldCoprocess(argv, extraEnv []string) *HeldCoprocessTransport {
	return &HeldCoprocessTransport{argv: argv, env: extraEnv}
}

func (t *HeldCoprocessTransport) start() error {
	if len(t.argv) == 0 {
		return fmt.Errorf("no engine command")
	}
	cmd := exec.Command(t.argv[0], t.argv[1:]...)
	cmd.Stderr = os.Stderr
	if len(t.env) > 0 {
		cmd.Env = append(os.Environ(), t.env...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	t.cmd, t.enc, t.dec, t.started = cmd, json.NewEncoder(stdin), json.NewDecoder(stdout), true
	return nil
}

func (t *HeldCoprocessTransport) RoundTrip(req Request) (Response, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.started {
		if err := t.start(); err != nil {
			return Response{}, err
		}
	}
	if err := t.enc.Encode(req); err != nil {
		return Response{}, err
	}
	var resp Response
	if err := t.dec.Decode(&resp); err != nil {
		return Response{}, err
	}
	return resp, nil
}

func (t *HeldCoprocessTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cmd != nil && t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
		_ = t.cmd.Wait()
		t.cmd, t.started = nil, false
	}
	return nil
}

// ExecTransport spawns the engine per op (one JSON request in, one response out). Simplest, but
// cold — it is the conformance baseline / fallback, not the warm path.
type ExecTransport struct {
	Argv []string
	Env  []string
}

func (t ExecTransport) RoundTrip(req Request) (Response, error) {
	if len(t.Argv) == 0 {
		return Response{}, fmt.Errorf("no engine command")
	}
	cmd := exec.Command(t.Argv[0], t.Argv[1:]...)
	cmd.Stderr = os.Stderr
	if len(t.Env) > 0 {
		cmd.Env = append(os.Environ(), t.Env...)
	}
	in, err := json.Marshal(req)
	if err != nil {
		return Response{}, err
	}
	cmd.Stdin = bytes.NewReader(append(in, '\n'))
	out, err := cmd.Output()
	if err != nil {
		return Response{}, err
	}
	var resp Response
	if err := json.Unmarshal(bytes.TrimSpace(out), &resp); err != nil {
		return Response{}, err
	}
	return resp, nil
}

func (ExecTransport) Close() error { return nil }

// ProtocolEngine is a mem.Engine that drives ANY breadth engine over the protocol via a Transport.
// turbovec plugs in here (as the sidecar binary behind a held coprocess); the pure-Go reference
// engine proves it with zero Rust. It enforces the identity guard (fail-CLOSED on an embedder
// mismatch), idempotent Index (no re-embed when the generation matches), and explicit error
// classes (unavailable → degrade; mismatch → refuse).
type ProtocolEngine struct {
	transport        Transport
	expectedEmbedder string // "" = adopt the engine's stamp on first use (bootstrap)

	probed   bool
	probeErr error
	caps     EngineCapabilities
	reqN     int
}

func NewProtocolEngine(t Transport, expectedEmbedder string) *ProtocolEngine {
	return &ProtocolEngine{transport: t, expectedEmbedder: expectedEmbedder}
}

func (e *ProtocolEngine) nextReqID() string {
	e.reqN++
	return "req_" + strconv.Itoa(e.reqN)
}

// ensureProbe asks the engine for capabilities once and runs the identity guard.
func (e *ProtocolEngine) ensureProbe() error {
	if e.probed {
		return e.probeErr
	}
	e.probed = true
	resp, err := e.transport.RoundTrip(Request{SchemaVersion: ProtocolVersion, RequestID: e.nextReqID(), Op: OpCapabilities})
	if err != nil || !resp.OK {
		e.probeErr = ErrEngineUnavailable
		return e.probeErr
	}
	if e.expectedEmbedder != "" && resp.Embedder != e.expectedEmbedder {
		e.probeErr = fmt.Errorf("%w: indexed with %q, engine reports %q", ErrEmbedderMismatch, e.expectedEmbedder, resp.Embedder)
		return e.probeErr
	}
	e.caps = EngineCapabilities{Name: resp.Name, Breadth: resp.Breadth, Embedder: resp.Embedder, CorpusGen: resp.CorpusGen}
	return nil
}

func (e *ProtocolEngine) Capabilities() EngineCapabilities {
	if err := e.ensureProbe(); err != nil {
		// Optimistic: claim breadth so QueryBreadth proceeds; the real error (unavailable /
		// mismatch) surfaces loudly on Index/Query rather than masquerading as ErrUnsupported.
		return EngineCapabilities{Name: "protocol", Breadth: true}
	}
	return e.caps
}

func (e *ProtocolEngine) Index(claims []Claim, gen int64) error {
	if err := e.ensureProbe(); err != nil {
		return err
	}
	if e.caps.CorpusGen == gen { // idempotent: engine already at this generation
		return nil
	}
	cts := make([]ClaimText, 0, len(claims))
	for _, c := range claims {
		cts = append(cts, ClaimText{ClaimID: c.ID, Text: ClaimIndexText(c)})
	}
	resp, err := e.transport.RoundTrip(Request{SchemaVersion: ProtocolVersion, RequestID: e.nextReqID(), Op: OpIndex, CorpusGen: gen, Claims: cts})
	if err != nil {
		return ErrEngineUnavailable
	}
	if !resp.OK {
		return wireErr(resp)
	}
	e.caps.CorpusGen = gen
	return nil
}

func (e *ProtocolEngine) Query(text, gate string, allowlist []string, k int) ([]Hit, error) {
	if err := e.ensureProbe(); err != nil {
		return nil, err
	}
	resp, err := e.transport.RoundTrip(Request{SchemaVersion: ProtocolVersion, RequestID: e.nextReqID(), Op: OpQuery, Text: text, Gate: gate, Allowlist: allowlist, K: k})
	if err != nil {
		return nil, ErrEngineUnavailable
	}
	if !resp.OK {
		return nil, wireErr(resp)
	}
	return resp.Hits, nil
}

func (e *ProtocolEngine) Close() error { return e.transport.Close() }

func wireErr(resp Response) error {
	if resp.Error == nil {
		return fmt.Errorf("engine error (no detail)")
	}
	if resp.Error.Code == CodeEmbedderMismatch {
		return fmt.Errorf("%w: %s", ErrEmbedderMismatch, resp.Error.Message)
	}
	return fmt.Errorf("engine error [%s]: %s", resp.Error.Code, resp.Error.Message)
}
