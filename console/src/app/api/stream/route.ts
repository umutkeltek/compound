import { watch, type FSWatcher } from "node:fs";
import { open, readFile, stat } from "node:fs/promises";
import path from "node:path";

import { COMPOUND_HOME, privateFallbacksEnabled } from "@/lib/compound";
import { feedLines, parseWorkEventLine, resolveReadOffset } from "@/lib/stream/tail-parser";

/*
 * Live envelope-spine stream (DESIGN.md §9.6 "Information flow") — Server-Sent
 * Events tailing $COMPOUND_HOME/events/work.jsonl from a byte cursor, plus a
 * lightweight "memory-pulse" whenever $COMPOUND_HOME/ledger/seq changes.
 * GET only, no mutation, ever.
 *
 * Raw tailing has no `compound … --json` verb behind it (there is no
 * streaming CLI surface), so — matching every other raw ledger/projection
 * read in this console (route-evidence.ts, map/projections.ts,
 * memory-data.ts) — it is gated behind privateFallbacksEnabled(). Disabled:
 * the connection still opens and stays alive (a heartbeat only), so the
 * client hook never sees a hard failure; it just never receives transition
 * events and the ticker stays in its honest empty state.
 */

export const dynamic = "force-dynamic";

const WORK_EVENTS_FILE = path.join(COMPOUND_HOME, "events", "work.jsonl");
const SEQ_FILE = path.join(COMPOUND_HOME, "ledger", "seq");

const BACKLOG_LINES = 25;
const EVENTS_POLL_MS = 2_000; // slow fallback alongside fs.watch (watch is unreliable on some FS/host combos)
const SEQ_POLL_MS = 4_000;
const HEARTBEAT_MS = 25_000;

function sseFrame(event: string, data: unknown, id?: string): string {
  const lines = [`event: ${event}`];
  if (id) lines.push(`id: ${id}`);
  lines.push(`data: ${JSON.stringify(data)}`, "", "");
  return lines.join("\n");
}

async function readSeq(): Promise<string | null> {
  try {
    return (await readFile(SEQ_FILE, "utf8")).trim();
  } catch {
    return null;
  }
}

async function readSlice(file: string, position: number, length: number): Promise<string> {
  if (length <= 0) return "";
  const handle = await open(file, "r");
  try {
    const buffer = Buffer.alloc(length);
    const { bytesRead } = await handle.read(buffer, 0, length, position);
    return buffer.subarray(0, bytesRead).toString("utf8");
  } finally {
    await handle.close();
  }
}

export async function GET(request: Request) {
  const encoder = new TextEncoder();
  let closed = false;
  const timers: Array<ReturnType<typeof setInterval>> = [];
  let watcher: FSWatcher | null = null;

  const stream = new ReadableStream<Uint8Array>({
    async start(controller) {
      const send = (chunk: string) => {
        if (closed) return;
        try {
          controller.enqueue(encoder.encode(chunk));
        } catch {
          /* client already gone; abort handler below finishes the cleanup */
        }
      };

      const cleanup = () => {
        if (closed) return;
        closed = true;
        for (const timer of timers) clearInterval(timer);
        watcher?.close();
        try {
          controller.close();
        } catch {
          /* already closed */
        }
      };
      request.signal.addEventListener("abort", cleanup);

      send(`retry: 3000\n\n`);

      if (!privateFallbacksEnabled()) {
        send(sseFrame("ready", { streaming: false }));
        timers.push(setInterval(() => send(`: heartbeat\n\n`), HEARTBEAT_MS));
        return;
      }

      send(sseFrame("ready", { streaming: true }));

      let offset = 0;
      let remainder = "";

      // Seed the backlog so first paint isn't empty (mirrors useLensData's
      // SSR-seed pattern), then set the cursor to end-of-file so live tailing
      // picks up exactly where the backlog left off — no duplicate emission.
      try {
        const st = await stat(WORK_EVENTS_FILE);
        const text = await readSlice(WORK_EVENTS_FILE, 0, st.size);
        offset = st.size;
        const lines = text.split("\n").filter((l) => l.trim().length > 0);
        for (const line of lines.slice(-BACKLOG_LINES)) {
          const parsed = parseWorkEventLine(line);
          if (parsed) send(sseFrame("transition", parsed, `${parsed.ts}:${parsed.work_id}:${parsed.to}`));
        }
      } catch {
        offset = 0; // file not written yet: tailing below picks it up once it exists
      }

      let lastSeq = await readSeq();

      const tailEvents = async () => {
        let size: number;
        try {
          size = (await stat(WORK_EVENTS_FILE)).size;
        } catch {
          return; // file still absent
        }
        const resolved = resolveReadOffset(offset, size);
        if (resolved.truncated) remainder = ""; // rotation/compaction: drop any half-buffered line

        if (size <= resolved.offset) {
          offset = resolved.offset;
          return;
        }

        const chunk = await readSlice(WORK_EVENTS_FILE, resolved.offset, size - resolved.offset);
        offset = size;
        const { lines, remainder: nextRemainder } = feedLines(remainder, chunk);
        remainder = nextRemainder;
        for (const line of lines) {
          const parsed = parseWorkEventLine(line);
          if (parsed) send(sseFrame("transition", parsed, `${parsed.ts}:${parsed.work_id}:${parsed.to}`));
        }
      };

      const tailSeq = async () => {
        const seq = await readSeq();
        if (seq !== null && seq !== lastSeq) {
          lastSeq = seq;
          send(sseFrame("memory-pulse", { seq }));
        }
      };

      try {
        watcher = watch(path.dirname(WORK_EVENTS_FILE), (_type, filename) => {
          if (filename === path.basename(WORK_EVENTS_FILE)) tailEvents().catch(() => {});
        });
      } catch {
        watcher = null; // fs.watch unsupported here: the poll fallback below still covers it
      }

      timers.push(setInterval(() => tailEvents().catch(() => {}), EVENTS_POLL_MS));
      timers.push(setInterval(() => tailSeq().catch(() => {}), SEQ_POLL_MS));
      timers.push(setInterval(() => send(`: heartbeat\n\n`), HEARTBEAT_MS));
    },
    cancel() {
      closed = true;
      for (const timer of timers) clearInterval(timer);
      watcher?.close();
    },
  });

  return new Response(stream, {
    headers: {
      "Content-Type": "text/event-stream",
      "Cache-Control": "no-cache, no-transform",
      Connection: "keep-alive",
      "X-Accel-Buffering": "no",
    },
  });
}
