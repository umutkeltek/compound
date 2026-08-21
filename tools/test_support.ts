import { spawnSync } from "node:child_process";

export function initializeCompoundHome(
  compoundBin: string,
  homePath: string,
): Record<string, string | undefined> {
  const env = { ...process.env, COMPOUND_HOME: homePath };
  const result = spawnSync(compoundBin, ["home", "init"], {
    cwd: process.cwd(),
    env,
    encoding: "utf8",
  });
  if (result.error) throw result.error;
  if (result.status !== 0) {
    throw new Error(
      `${compoundBin} home init failed with status ${result.status}\nstdout:\n${result.stdout}\nstderr:\n${result.stderr}`,
    );
  }
  return env;
}
