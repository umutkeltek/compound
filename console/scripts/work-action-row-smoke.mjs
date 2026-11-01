import { runWorkActionRowSelfTest } from "../src/components/work/work-action-row.selftest.tsx";
import { runWorkLifecycleStatusSelfTest } from "../src/components/work/work-lifecycle-status.selftest.tsx";

const results = [runWorkActionRowSelfTest(), runWorkLifecycleStatusSelfTest()];
for (const result of results) {
  for (const line of result.details) console.log(line);
}
if (results.some((result) => !result.ok)) {
  console.error("work-action-row smoke FAILED");
  process.exit(1);
}
console.log("work-action-row smoke ok: copy-only transitions and honest work lifecycle views");
