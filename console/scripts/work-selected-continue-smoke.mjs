import { runSelectedContinueSelfTest } from "../src/lib/work-selected-continue.selftest.ts";

const result = runSelectedContinueSelfTest();
for (const line of result.details) console.log(line);
if (!result.ok) process.exit(1);
console.log("work-selected-continue smoke ok");
