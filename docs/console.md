# Console — the visual surface for Compound

> The console is a **local, operator-owned** web dashboard on port 3777. It
> reads from `COMPOUND_HOME` through the `compound` CLI — it never owns
> meaning and never becomes a second authority. It is a **projection**: a
> rebuildable view over the spine's objects. The default launcher leaves both
> mutations and private fallbacks disabled. `--mutations` is an explicit local
> API-extension opt-in; even then, mutation routes require a same-origin
> localhost request carrying an out-of-band 64-hex-character capability in
> `x-compound-console-capability`. The standard browser never receives that
> capability and remains projection-only by default. An explicit local operator
> connection can use a separate, bounded browser session as described below.
> Direct private document reads are
> unavailable; use a Compound-owned projection or scoped CLI read surface.

## Setup

The dashboard is optional. It is **not included in the prebuilt binary archives**.
Use the `console/` directory from the matching reviewed source commit, plus
Bun and Node 20+. Keep the source checkout separate from your runtime home.
Installing dependencies uses the package registry; no provider/model call is
needed to render the dashboard.

From the source checkout:

```sh
cd console
bun install --frozen-lockfile
export COMPOUND_HOME="/absolute/path/to/your-existing-compound-home"
compound home doctor
compound console build --operator --require-explicit-home
compound console serve --operator --require-explicit-home
```

Open the local address printed by the launcher. Keep the terminal open; Ctrl-C
stops this owned server. In a second terminal with the **same home**,
`compound console status --json` checks identity, reachability and static assets.
Do not start a second server to resolve a port conflict.

For development, `compound console --operator --require-explicit-home` starts
the hot-reload lifecycle instead. The `bun run dev`, `bun run build` and
`bun run serve` package scripts also use the owned launcher, but invoke it
through `go run`: **those scripts require Go** as well as an explicit
`COMPOUND_HOME`.

The `--operator` flag selects this local lifecycle; it grants no mutation
authority. The supported browser stays read-only. A card's copy-command is
guidance for your harness or terminal, not evidence that the action ran.

To include branch/worktree inventory on the Agents view, set
`COMPOUND_WORKTREE_REPO="/absolute/path/to/your-project"` before starting the
console. This read-only inventory binding is separate from the dashboard source
checkout and installed-binary freshness check. The console never guesses a worktree target
from its own working directory. Inventory shows the existing CLI's classifications,
dirty/unlanded work, ownership, keep decisions and liveness uncertainty. It has
no delete button; retirement still requires its governed exact-plan review.

### Unsupported mutation extension

A trusted local operator can opt into the existing capability-gated API extension
outside the supported public-alpha browser surface:

```sh
export COMPOUND_HOME="/absolute/path/to/operator-home"
export COMPOUND_CONSOLE_CAPABILITY="$(openssl rand -hex 32)"
compound console --operator --mutations --require-explicit-home
```

A direct extension client must send that exact 64-hex-character capability only
in the `x-compound-console-capability` header on a same-origin loopback request.
The standard browser never receives it; Compound issues no capability cookies
and omits the value from launch JSON. Missing or malformed configuration fails
closed. This extension is not needed for ordinary dashboard setup.

### Optional local operator connection

`--operator-session` adds a separate browser connection to an explicit dev or
serve lifecycle. It does not change the default launch, policy decisions,
standing grants, or confirmation requirements. It requires an explicit home
and uses the native broker without a legacy capability:

```sh
compound console serve --operator --operator-session --require-explicit-home
```

Open **http://127.0.0.1:3777**. In an interactive terminal with the same explicit
`COMPOUND_HOME`, run `compound console pair`, then enter its one-use code in
the **Browser connection** card on Settings ("For agents: how to connect"). The
connection is not used for anything yet. The launcher never prints pairing codes into its service
logs. Redirected pair output is refused; an explicit `--out NEW_PRIVATE_FILE`
creates a new private file and refuses an existing file or symlink. Codes expire
after five minutes, and at most three may be outstanding. Run `pair` again when
a code expires or after disconnecting.

The supervisor owns pairing and sessions behind a user-owned Unix socket. All
Next workers share that owner, so concurrent code redemption has one winner.
The browser receives a fresh opaque HttpOnly, SameSite=Strict cookie, never the
server capability or broker credential. Each session is bound to the pinned home,
exact loopback origin and supervisor instance, expires after one hour without
extension, and can be revoked with **Disconnect**. Restarting the supervisor
invalidates all codes and sessions. At most four sessions can be outstanding.
An action already admitted before disconnect may finish; later admissions fail.

The new browser transport accepts only current owner-issued `generic-action-v2`
actions carrying their exact ActionIntent and authority revision. Every submit
independently checks the live native session, exact Host and Origin, browser
fetch context, CSRF token and fresh native action authority. Next's internal
loopback URL normalization is accepted only alongside the unchanged canonical
browser Host and Origin; other browser hostnames and ports remain refused. A connected label
alone grants no permission. Unsupported legacy forms, memory promotion, grant
editing and unstamped controls remain unavailable through this connection.
The existing capability extension keeps its separate authentication gate. Adding
`--mutations` explicitly opts into that extension too and requires its configured
capability; session-only launches strip any ambient legacy capability.

The runtime socket directory and lock are owner-only temporary transport state;
no capability or session descriptor is persisted. Shutdown removes the owned
socket and destroys all in-memory authentication state, while retaining the
empty lock primitive so a concurrent process cannot create a second owner by
replacing its lockfile. A stopped or unreachable broker fails closed.

### Work-attached visual campaigns

Work detail reads its own native record first. Supplemental overview data loads
through the shared background reader, with refresh and stale/error evidence;
a slow overview collector does not delay the task's goal, status or next step.

App and work detail pages read visual campaigns attached to native WorkEnvelope
artifacts. Discovery covers open and recent work, so a campaign remains findable
after its work closes. Images are served through the campaign asset adapter,
which checks their declared hashes and confines paths to the campaign's owned
artifact directory. Missing or changed images remain explicit gaps.

Campaign material stays draft when localization, release evidence or required
tests are incomplete. A visible image or a completed generation run does not
claim the app was released or the campaign improved results; those outcomes
still need their own native evidence and checks.

## What you'll see

The current left rail is a stable, needs-led map. Backend commands and
projections appear inside the destination that owns their operator question;
they do not automatically become new navigation rows.

| Destination | Operator question | Context it owns |
|---|---|---|
| **Today** | What deserves my attention now? | Cross-domain attention and recently proven change |
| **Live** | What is observed moving, waiting, stale, or unreachable? | RunAttempts, heartbeats, executors, leases, and coordination |
| **Work** | What is moving, blocked, ready, or waiting for proof? | WorkEnvelopes, ownership, lifecycle, Receipts, and Transitions |
| **Decide** | Which consequential calls need my judgment? | Evidence, consequence, reversibility, authority, and safe actions |
| **Knowledge** | What does Compound know, why, and how current is it? | Claims, candidates, positions, patterns, Sources, and provenance |
| **System** | What technical and authority configuration is effective? | Capabilities, routing, hooks, health, Settings, and Standing law |
| **Outcomes** | What external change fed learning back into the system? | Product signals, hypotheses, reviews, learned next moves, and paid acquisition (Ads: spend, CPA, ROAS per platform, product, campaign — see [`docs/operations/ads-signals.md`](operations/ads-signals.md)) |

Search is the one global overlay rather than another destination. A
Live row opens the same WorkEnvelope in Work; Today links to the owning Work or
decision record; evidence links to its Knowledge source; contextual authority
links to the full System record.

The binding context-ownership and progressive-disclosure rules live in
[`console/DESIGN.md`](../console/DESIGN.md#10-experience-architecture--simple-first-depth-on-demand).

## Design principles

The console follows three orthogonal axes (see `console/DESIGN.md`):

| Axis | Question | Visual encoding |
|---|---|---|
| **Domain** | Which subsystem? | Cool hues (blue, violet, cyan) — accent only, never fill |
| **State** | Where in lifecycle? | Neutral + blue-live + warm trio — the pill on each card |
| **Severity** | How alarming? | Green / amber / red — health tiles, meters, banners |

Amber-as-fill is reserved for one thing: **the admission seam** — a card that
needs the operator gets a 3px amber left seam. Scanning left edges down a page
answers "what needs me."

## Architecture

```text
Browser
  → Next.js SSR (console/src/app/)
    → compound CLI (subprocess calls, e.g. `compound workbench --json`)
      → COMPOUND_HOME (read-only)
```

- The supported standard console **never writes** to `COMPOUND_HOME`. It is a
  read-only projection.
- All data comes through the `compound` CLI as JSON subprocess calls.
- The unsupported mutation API extension, when explicitly enabled, goes
  through the same policy-gated seams as the CLI.
- The console is rebuildable: delete it, re-run `compound console --operator`,
  and it regenerates from the same spine objects.

## Live stream

The console includes a server-sent events (SSE) stream at `/api/stream` that
tails the work-event log and ledger in real time. New receipts, transitions,
and memory updates appear without refresh.

```text
compound stream --follow   → SSE frames → console auto-updates
```

## Console health check

```sh
compound console status --json
```

Verifies: the explicitly pinned console identity, bounded HTTP response, and
static assets named by the current build. `data_freshness: not_checked` is
deliberate: a healthy service status does not assert that attention, home
doctor, workbench, or other dashboard projections are current. Older consoles
without the readiness route report readiness unavailable; they are not
declared healthy from a stalled root response.

## Capability non-disclosure canary

Before certifying a release candidate, run:

```sh
node console/scripts/capability-profile-canary.mjs
```

The canary builds the candidate binary, initializes a throwaway home, performs
a sanitized production build, and launches real Next development and
production profiles on an ephemeral loopback port with a nonce capability. It
checks HTML, RSC payloads, every referenced static asset, mutation error
responses, cookies, and captured process output for raw or encoded capability
or legacy-session material. It also proves that missing, wrong, cookie-only,
query-only, and body-only credentials fail closed while the exact header reaches
request validation without executing a mutation.

## Understanding health

Today and Settings distinguish a live result from a last-known result and an
unreachable feed. Missing or incomplete doctor data means **health unknown**,
not an empty healthy home. A last-known passing check is not current acceptance.
A doctor command that exits nonzero may appear as unavailable rather than a
parsed failure report; run the displayed command to see its full diagnostic.

Memory carries the same read status through its proposed queue, standing
decisions, source evidence and lifecycle counts. Whole-home memory projections
use the existing bounded heavy-read budget. Only a successful live empty read
may show an empty-state claim; unavailable counts remain unknown, and retained
last-good rows disclose their stale state with exact read details available.
The dedicated memory-read honesty smoke is a console-CI gate, and a canonical
gate whenever the proved range touches `console/` or `--with-console` is passed;
it is classified in the same exact inventory as the other console smokes. It runs
in its own process so the fixture can pin its isolated home and binary before
any loader resolves runtime identity.

Settings distinguishes observation-only hooks, missing installation, missing
handlers and wrapper requirements. Fixture support is not installed enforcement
proof. Recovery commands begin with verification or a dry-run; restoration needs
an archive and a **new, absent absolute target directory**, and does not activate
that target. Follow [Recovery](operations/recovery.md) before changing a live home.

## Troubleshooting

| Symptom | Fix |
|---|---|
| Port 3777 in use | Check `compound console status --json`; stop only the server you own from its terminal. Do not kill an unknown process. |
| Blank page | Run `compound home doctor` and check the launcher output and selected home. |
| No data on briefing | Check whether the feed is live, stale or unreachable. Only a live empty projection means no data yet. |
| Build errors | Check Bun/Node and run `bun install --frozen-lockfile` in the matching console checkout. |
| Stale data | Last-known results are retained when refresh fails. Run the displayed source command and check the home/binary identity. |


## Projects and focused relationships

The Projects page is retired. Work's "By app" view groups all open work by app,
codebase and "not linked", in one order (most items that need you first). Each group
shows how many items need you, are in progress, wait for a proof check, and have not
moved in a week; every count opens the Work List of exactly those items, and an app's
row links to its app page. The List keeps the group in its address (`app=<slug>` or
`project=<group id>`), so a filtered list is a link that can be shared and reloaded.

Membership comes from one resolver over the app catalog and product inventory: the
record's own product id first, then the folders an app's code lives in, then folder
and surface names. Task text never creates membership, and work that names no app or
codebase stays visible in its own group instead of being dropped or guessed.

Old `/projects` links still work: an app's scope opens that app's page, an opened work
item opens its work page, and anything else opens "By app".
