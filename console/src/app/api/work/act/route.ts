import { compoundFailure, runCompound } from "@/lib/compound";
import { workActionArgv } from "@/lib/work-data";

const WORK_ID_RE = /^work_[a-z0-9]{6,}$/;

export async function POST(request: Request) {
  let body: Record<string, unknown>;
  try {
    body = (await request.json()) as Record<string, unknown>;
  } catch {
    return Response.json({ ok: false, stderr: "invalid JSON body" }, { status: 400 });
  }

  const id = typeof body.id === "string" ? body.id.trim() : "";
  const action = body.action;

  if (!WORK_ID_RE.test(id)) {
    return Response.json({ ok: false, stderr: "invalid work id" }, { status: 400 });
  }
  if (action !== "admit" && action !== "cancel") {
    return Response.json({ ok: false, stderr: "invalid work action" }, { status: 400 });
  }

  const args = workActionArgv(id, action);
  try {
    const result = await runCompound(args);
    return Response.json({ ok: true, stdout: result.stdout, stderr: result.stderr });
  } catch (error) {
    const failure = compoundFailure(error);
    return Response.json({
      ok: false,
      stdout: failure.stdout,
      stderr: failure.stderr || failure.message,
    });
  }
}
