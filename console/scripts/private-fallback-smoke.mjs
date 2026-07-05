import assert from "node:assert/strict";

const oldHome = process.env.COMPOUND_HOME;
const oldFallback = process.env.COMPOUND_CONSOLE_PRIVATE_FALLBACKS;

try {
  process.env.COMPOUND_HOME = "/tmp/compound-private-fallback-smoke";
  delete process.env.COMPOUND_CONSOLE_PRIVATE_FALLBACKS;

  const { reviewCommand, reviewSentence } = await import("../src/lib/compound.ts");
  const row = { area: "external-repo-intake", id: "abc123" };

  assert.equal(reviewSentence(row), "External repo awaits a projected review action.");
  assert.equal(reviewCommand(row), "compound reviews --area external-repo-intake");
  assert.equal(reviewCommand(row).includes(process.env.COMPOUND_HOME), false);

  process.env.COMPOUND_CONSOLE_PRIVATE_FALLBACKS = "1";
  assert.equal(
    reviewCommand(row),
    "compound capability external-intake-review /tmp/compound-private-fallback-smoke/reviews/external-repo-intake/abc123.review.yaml",
  );
} finally {
  if (oldHome === undefined) delete process.env.COMPOUND_HOME;
  else process.env.COMPOUND_HOME = oldHome;

  if (oldFallback === undefined) delete process.env.COMPOUND_CONSOLE_PRIVATE_FALLBACKS;
  else process.env.COMPOUND_CONSOLE_PRIVATE_FALLBACKS = oldFallback;
}

console.log("private fallback smoke ok: runtime paths stay opt-in");
