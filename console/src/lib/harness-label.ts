/**
 * One name per agent. Records spell the same agent many ways: an owner or
 * claimed-by with a lane (`claude_code:cleanup-1`) or model
 * (`oh_my_pi/model-x`) suffix, a role prefix (`harness:claude-code`), a
 * harness id with a hyphen or an underscore, a label in any case. `agentKey`
 * folds them into one key for grouping and filtering; `agentDisplayName`
 * gives the one name to show. Names come from the data (the executors
 * projection's ids, labels and aliases); this file carries no agent names.
 *
 * Client-safe and dependency-free: no reads, no imports.
 */

/** The key for "no agent": an empty owner, or the executors projection's `unassigned` bucket. */
export const NO_AGENT_KEY = "unassigned";

/** The key for the operator: `operator`, `operator:<name>`, `operator-<surface>`. */
export const OPERATOR_AGENT_KEY = "operator";

/**
 * One agent as the executors projection (`compound executors --json`)
 * describes it: its id, its label, and the other names it goes by. Pass
 * these so aliases (`claude` for `claude_code`) fold into the agent's key and
 * the projection's label is the name shown.
 */
export type AgentDirectoryEntry = { readonly id: string; readonly label?: string; readonly aliases?: readonly string[] };

/** Prefixes that name a role, not an agent: `harness:<agent>`, `owner:<agent>`. */
const ROLE_PREFIXES = new Set(["harness", "owner"]);

/** Spelled like the CLI's executor ids: lowercase, spaces and hyphens as `_`. */
function normalizeAgentName(value: string): string {
  return value.trim().toLowerCase().replace(/[\s_-]+/g, "_").replace(/^_+|_+$/g, "");
}

/** Preserve raw RunAttempt evidence while making fixture execution clear. */
export function harnessDisplayName(raw: string): string {
  if (!raw) return "Execution detail unavailable";
  if (raw.toLowerCase() === "fake") return "Fixture adapter";
  return raw.replace(/[-_]+/g, " ").replace(/\b\w/g, (letter) => letter.toUpperCase());
}

/**
 * The stable key of the agent behind any raw owner, claimed-by, actor or
 * harness string. `Claude Code`, `claude-code`, `claude_code`,
 * `claude_code:cleanup-1` and `harness:claude-code` all give `claude_code`,
 * the same spelling as the executors projection's id. `operator` in any form
 * gives OPERATOR_AGENT_KEY; empty or `unassigned` gives NO_AGENT_KEY. With a
 * directory, a name listed as an agent's alias gives that agent's id.
 * Compare keys, never raw strings, when grouping or filtering by agent.
 */
export function agentKey(raw: string | null | undefined, directory?: readonly AgentDirectoryEntry[]): string {
  let value = (raw ?? "").trim();
  const parts = value.split(":");
  if (parts.length > 1 && ROLE_PREFIXES.has(parts[0].trim().toLowerCase()) && parts[1].trim()) {
    value = parts.slice(1).join(":");
  }
  const key = normalizeAgentName(value.split(/[:/]/)[0] ?? "");
  if (!key || key === NO_AGENT_KEY) return NO_AGENT_KEY;
  if (key === OPERATOR_AGENT_KEY || key.startsWith(`${OPERATOR_AGENT_KEY}_`)) return OPERATOR_AGENT_KEY;
  for (const entry of directory ?? []) {
    const id = normalizeAgentName(entry.id);
    if (!id) continue;
    if (id === key || (entry.aliases ?? []).some((alias) => normalizeAgentName(alias) === key)) {
      return id === NO_AGENT_KEY ? NO_AGENT_KEY : id;
    }
  }
  return key;
}

/**
 * The one name to show for an agent, from any raw form. "No agent" for no
 * owner, "You" for the operator, the executors projection's label when the
 * directory has the agent, else the key in words (`claude_code` → "Claude
 * Code"). Keep the raw string as small secondary text when it helps to find
 * the lane.
 */
export function agentDisplayName(raw: string | null | undefined, directory?: readonly AgentDirectoryEntry[]): string {
  const key = agentKey(raw, directory);
  if (key === NO_AGENT_KEY) return "No agent";
  if (key === OPERATOR_AGENT_KEY) return "You";
  const entry = (directory ?? []).find((candidate) => normalizeAgentName(candidate.id) === key);
  const label = entry?.label?.trim();
  return label || harnessDisplayName(key);
}

/**
 * The operator's own names, learned from the data: every `<name>` seen in an
 * `operator:<name>` form. Add them as aliases of an OPERATOR_AGENT_KEY
 * directory entry so a record owned by the bare name (`<name>`) groups with
 * the operator instead of showing as a separate agent.
 */
export function operatorNames(raws: Iterable<string | null | undefined>): string[] {
  const names = new Set<string>();
  for (const raw of raws) {
    const [role, name] = (raw ?? "").split(":");
    if (normalizeAgentName(role ?? "") === OPERATOR_AGENT_KEY && name?.trim()) names.add(name.trim());
  }
  return [...names].sort();
}
