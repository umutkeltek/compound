#!/usr/bin/env bun
// STUB — implementation lands in the green commit. Falsifiers red against this.
export type BuildOptions = { product: string; capturedAt?: string };
export type EmitOptions = BuildOptions & { homePath: string };

export function buildPlaytestBatch(_runDir: string, _opts: BuildOptions): never {
  throw new Error("not implemented");
}

export function emitPlaytestObservations(_runDir: string, _opts: EmitOptions): never {
  throw new Error("not implemented");
}
