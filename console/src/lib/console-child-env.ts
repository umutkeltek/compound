const CONSOLE_CHILD_SECRET_KEYS = [
  "COMPOUND_CONSOLE_CAPABILITY",
  "COMPOUND_CONSOLE_SESSION_TOKEN",
  "COMPOUND_CONSOLE_OPERATOR_SESSION",
  "COMPOUND_CONSOLE_OPERATOR_SOCKET",
  "COMPOUND_CONSOLE_OPERATOR_BROKER_KEY",
  "COMPOUND_CONSOLE_OPERATOR_INSTANCE",
] as const;

/**
 * Construct the environment for every subprocess launched by the console.
 * The console capability authorizes the HTTP mutation extension itself; it is
 * never ambient authority for a spawned CLI process. The retired session-token
 * value is stripped at the same boundary so old operator shells cannot revive
 * it through a child.
 */
export function consoleChildEnv(overrides: Partial<NodeJS.ProcessEnv> = {}): NodeJS.ProcessEnv {
  const env: NodeJS.ProcessEnv = { ...process.env, ...overrides };
  for (const key of CONSOLE_CHILD_SECRET_KEYS) delete env[key];
  return env;
}
