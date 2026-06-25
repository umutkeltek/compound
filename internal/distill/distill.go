package distill

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Prompts ported faithfully from the proven TS distiller. Reasoning, not
// summarization; strict source attribution; hard verbatim preservation.

const distillSystem = `You are distilling an AI agent work-session into durable insight for the user's knowledge ledger. This is REASONING, not summarization — go from noise to insight: identify what actually MATTERS and why; do not just shrink text.
SOURCE ATTRIBUTION (this is an agent session): USER turns are the human/operator (harness injections already stripped). ASSISTANT turns are the AI agent, NOT the user — never attribute the agent's words to the user.
Preserve, word-for-word, BOTH the user's verbatim quotes AND specific factual findings (names, ids, provenance strings, file/commit refs, numbers) — a later synthesis pass depends on them; anything you generalize is lost permanently.
Produce typed, signal-separated output: SELECT (the user's decisions), SEE (what was surfaced + the user's questions), TELL (the agent's substantive conclusions). Be exhaustive on real insight, terse on mechanics. Output structured markdown.`

const mapSuffix = `

This is chunk {i} of {n} of a longer session. Distill THIS chunk's typed insights now; be exhaustive on real insight.`

const reduceSystem = `You are the SYNTHESIS pass of a reasoning-based distiller for the user's knowledge ledger. You receive per-chunk typed distillations from one continuous work-session. Your job is CROSS-CHUNK REASONING — connect threads across the whole session, resolve what the user actually decided vs merely floated, and name durable patterns.
VERBATIM PRESERVATION (HARD RULE): the chunk distillations already contain the user's verbatim quotes and specific factual findings. Carry EVERY one through WORD-FOR-WORD. Never paraphrase a quote, never merge two quotes, never thin a concrete fact into a generalized theme. Cross-chunk reasoning ADDS connective insight AROUND preserved quotes/facts; it never replaces them.
SOURCE ATTRIBUTION: USER = human/operator, ASSISTANT = agent. Output structured markdown with SELECT/SEE/TELL plus named patterns.`

const ledgerExtractSystem = `You convert a session distillation into a DECISION LEDGER — only durable decisions worth recalling later.
EVIDENCE CONTRACT (do NOT launder exploration into decisions): include an item ONLY if the user actually committed — explicit decision language ("we decided", "lock this", "let's do X", "ship it"), an accepted recommendation ("yes", "do that", "go ahead", "approved"), a stated standing preference ("I always", "by default", "never"), or a correction ("no, do it this way"). A hypothetical / question / "maybe" / the agent's own conclusion is NOT a user decision — DROP it.
COVERAGE: be COMPREHENSIVE on durable decisions; capture EVERY strategic/architectural/standing decision; if one decision has two clauses, capture both. MATERIALITY: drop one-off operational approvals that won't matter next week unless they encode a standing choice. FINDABILITY: each statement must NAME the concrete entities/systems it is about.
Output ONLY a JSON array (no prose, no code fence) of objects: {"statement": "...", "confidence": "high|medium|low", "domain": "short-slug", "quote": "verbatim user quote"}. If there are no durable user decisions, output [].`

const chunkChars = 220_000

func chunkOnTurns(turns []Turn) []string {
	var chunks, buf []string
	size := 0
	for _, t := range turns {
		blk := "[" + string(t.Role) + "] " + t.Text
		if size+len(blk) > chunkChars && len(buf) > 0 {
			chunks = append(chunks, strings.Join(buf, "\n\n"))
			buf, size = nil, 0
		}
		buf = append(buf, blk)
		size += len(blk) + 2
	}
	if len(buf) > 0 {
		chunks = append(chunks, strings.Join(buf, "\n\n"))
	}
	return chunks
}

type Result struct {
	Distillation     string
	Clean            *CleanResult
	DecisionTurns    int
	Chunks           int
	Calls            int
	PromptTokens     int
	CompletionTokens int
	Model            string
}

type Options struct {
	Model      string
	MinWorth   int
	MapWorkers int
}

// Run cleans, worth-gates, then map-reduces a session into a final distillation.
func Run(ctx context.Context, path string, opt Options) (*Result, error) {
	if opt.Model == "" {
		opt.Model = "deepseek-reasoner"
	}
	if opt.MinWorth == 0 {
		opt.MinWorth = 3
	}
	if opt.MapWorkers == 0 {
		opt.MapWorkers = 3
	}

	c, err := Clean(path)
	if err != nil {
		return nil, err
	}
	dt := CountDecisionTurns(c.Turns)
	res := &Result{Clean: c, DecisionTurns: dt, Model: opt.Model}
	if dt < opt.MinWorth {
		return res, fmt.Errorf("below worth gate: %d decision-turns < %d", dt, opt.MinWorth)
	}

	llm, err := newLLM(opt.Model)
	if err != nil {
		return res, err
	}
	chunks := chunkOnTurns(c.Turns)
	res.Chunks = len(chunks)

	var mu sync.Mutex
	track := func(u usage) {
		mu.Lock()
		res.Calls++
		res.PromptTokens += u.prompt
		res.CompletionTokens += u.completion
		mu.Unlock()
	}

	if len(chunks) == 1 {
		content, u, err := llm.call(ctx, distillSystem,
			"Here is the full cleaned, role-tagged session. Distill it now per instructions.\n\n"+chunks[0])
		if err != nil {
			return res, err
		}
		track(u)
		res.Distillation = content
		return res, nil
	}

	// MAP — bounded concurrency, ordered partials.
	partials := make([]string, len(chunks))
	errs := make([]error, len(chunks))
	sem := make(chan struct{}, opt.MapWorkers)
	var wg sync.WaitGroup
	for i := range chunks {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			sys := distillSystem + strings.NewReplacer("{i}", fmt.Sprint(i+1), "{n}", fmt.Sprint(len(chunks))).Replace(mapSuffix)
			content, u, err := llm.call(ctx, sys,
				fmt.Sprintf("Chunk %d/%d of the cleaned session:\n\n%s", i+1, len(chunks), chunks[i]))
			if err != nil {
				errs[i] = err
				return
			}
			track(u)
			partials[i] = content
		}(i)
	}
	wg.Wait()
	for _, e := range errs {
		if e != nil {
			return res, e
		}
	}

	// REDUCE — hierarchical fold for many chunks, then a final synthesis.
	const reduceBatch = 8
	level := partials
	for len(level) > reduceBatch {
		var next []string
		for i := 0; i < len(level); i += reduceBatch {
			end := i + reduceBatch
			if end > len(level) {
				end = len(level)
			}
			content, u, err := llm.call(ctx, reduceSystem,
				"Synthesize these per-chunk distillations into one:\n\n"+strings.Join(level[i:end], "\n\n---\n\n"))
			if err != nil {
				return res, err
			}
			track(u)
			next = append(next, content)
		}
		level = next
	}
	content, u, err := llm.call(ctx, reduceSystem,
		"Here are per-chunk distillations of one continuous session. Synthesize the FINAL typed distillation now, reasoning across all chunks.\n\n"+strings.Join(level, "\n\n---\n\n"))
	if err != nil {
		return res, err
	}
	track(u)
	res.Distillation = content
	return res, nil
}
