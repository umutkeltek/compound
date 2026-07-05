import { readFile } from "node:fs/promises";
import path from "node:path";

import { COMPOUND_HOME, privateFallbacksEnabled } from "@/lib/compound";

const MAX_BYTES = 1024 * 1024;
const EVIDENCE_REF_RE = /^ev_[a-z0-9]+$/;

function inside(child: string, root: string): boolean {
  const rel = path.relative(root, child);
  return rel === "" || (!rel.startsWith("..") && !path.isAbsolute(rel));
}

function allowedRoots(): string[] {
  return [
    path.resolve(COMPOUND_HOME),
    path.resolve(process.cwd()),
    path.resolve(process.cwd(), ".."),
  ];
}

function resolveAllowedPath(raw: string): string | null {
  const resolved = path.resolve(raw);
  return allowedRoots().some((root) => inside(resolved, root)) ? resolved : null;
}

async function readBounded(filePath: string): Promise<{ content: string; truncated: boolean }> {
  const data = await readFile(filePath);
  const truncated = data.byteLength > MAX_BYTES;
  const slice = truncated ? data.subarray(0, MAX_BYTES) : data;
  return { content: slice.toString("utf8"), truncated };
}

async function readEvidence(ref: string) {
  const ledgerPath = path.resolve(COMPOUND_HOME, "ledger", "evidence.jsonl");
  const { content } = await readBounded(ledgerPath);
  for (const line of content.split("\n")) {
    if (!line.trim()) continue;
    try {
      const row = JSON.parse(line) as Record<string, unknown>;
      if (row.id !== ref) continue;
      return {
        title: `${row.id} · ${row.source_kind ?? "evidence"}`,
        content: typeof row.quote_text === "string" ? row.quote_text : JSON.stringify(row, null, 2),
        metadata: row,
      };
    } catch {
      continue;
    }
  }
  return null;
}

export async function GET(request: Request) {
  const url = new URL(request.url);
  const evidenceRef = url.searchParams.get("evidence_ref")?.trim() ?? "";
  const rawPath = url.searchParams.get("path")?.trim() ?? "";

  try {
    if (!privateFallbacksEnabled()) {
      return Response.json(
        {
          ok: false,
          stderr: "direct document reads disabled; set COMPOUND_CONSOLE_PRIVATE_FALLBACKS=1 for private local inspection",
        },
        { status: 403 },
      );
    }

    if (evidenceRef) {
      if (!EVIDENCE_REF_RE.test(evidenceRef)) {
        return Response.json({ ok: false, stderr: "invalid evidence ref" }, { status: 400 });
      }
      const evidence = await readEvidence(evidenceRef);
      if (!evidence) {
        return Response.json({ ok: false, stderr: "evidence ref not found" }, { status: 404 });
      }
      return Response.json({ ok: true, kind: "evidence", ...evidence });
    }

    if (!rawPath) {
      return Response.json({ ok: false, stderr: "path or evidence_ref required" }, { status: 400 });
    }

    const filePath = resolveAllowedPath(rawPath);
    if (!filePath) {
      return Response.json({ ok: false, stderr: "path outside allowed roots" }, { status: 403 });
    }

    const { content, truncated } = await readBounded(filePath);
    return Response.json({
      ok: true,
      kind: "file",
      title: filePath,
      content,
      truncated,
    });
  } catch (error) {
    const message = error instanceof Error ? error.message : "failed to read document";
    return Response.json({ ok: false, stderr: message }, { status: 500 });
  }
}
