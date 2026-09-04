# Go test profiling

The release gate remains `compound proof-check`. This profiler is diagnostic
only: it neither changes that gate nor mints a verification reference.

Capture fresh, per-test events outside the source tree. Preserve Go's exit
status before reading the profile; a successful summarizer is not proof that
the producer succeeded:

```bash
evidence_dir=$(mktemp -d)
go test -json -count=1 -timeout=40m ./... > "$evidence_dir/events.jsonl"
test_status=$?
go run ./scripts/testprofile "$evidence_dir/events.jsonl"
profile_status=$?
printf 'go-test exit=%s; profiler exit=%s; evidence=%s\n' "$test_status" "$profile_status" "$evidence_dir"
test "$test_status" -eq 0 && test "$profile_status" -eq 0
```

For a focused comparison, replace `./...` with the same package and `-run`
filter on both revisions, retaining `-count=1` (or the same repeat count).
Use matching Go versions, machine load, cache warmth and package parallelism;
record source SHAs and the original process exits beside the private evidence.
Do not run competing full suites while measuring latency.

The JSON ranks package durations and top-level test durations separately.
Nested test times overlap their parents; package durations overlap each other.
**Never sum these rows into total wall time or a promised speedup.** Repeated
runs remain separate samples. All failed subtests are retained, even when
short. Skips are counted; running/paused tests at EOF remain incomplete.
Malformed input still emits partial measurements and a parse error. Exit
codes are 0 for a complete passing input, 1 for failed/incomplete input, and
2 for input/output errors. These are diagnostic results, not certification.

The profiler's own behavior is covered by `go test ./scripts/testprofile`
and therefore by the unchanged canonical `go test ./...` gate.

## Turn measurements into cheaper execution

Choose the next change from observed cost: repeated build/setup, process
startup, storage work, polling, or contention. Reuse immutable binaries;
clone initialized homes into isolated mutable homes. Keep real-process tests
where process lifetime, exit codes, permissions or cancellation are the
behavior under test. Preserve failure-path assertions, not just happy paths.

The hook-enforcement tests use the existing suite-wide `compoundTestBinary`;
they do not share hook configs, captured responses, nonces or runtime homes.
Build reuse must not remove the genuine-response replay falsifier.

Future scheduling or exact-input proof reuse needs a separately reviewed
verification contract. A diagnostic profile cannot authorize either.
