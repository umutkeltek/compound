/*
 * The one work-id check for the console.
 *
 * It is the CLI's own rule (`mcpWorkIDPattern` in cmd/compound/mcp_transport.go):
 * the `work_` prefix, one letter or digit, then letters, digits, `.`, `_`, `:`
 * or `-`. Named ids such as `work_repo_audit_fixes_20261110` and
 * `work_delegation_trial_ops_20261106T02` are ordinary; a check that only
 * accepts the hashed `work_<hex>` shape refuses most of the board.
 *
 * The id is only ever handed to `compound` as one argv element, never to a
 * shell and never joined into a path, so the rule is about shape, not escaping.
 */
export const WORK_ID_PATTERN = /^work_[A-Za-z0-9][A-Za-z0-9._:-]*$/;

export function isWorkId(value: string): boolean {
  return WORK_ID_PATTERN.test(value);
}
