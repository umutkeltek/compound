/** Transport eligibility only. Native execution must still re-read the item and policy. */
export function isOperatorActionSupported(body: Record<string, unknown>): boolean {
  return typeof body.actionId === "string" && Boolean(body.actionId.trim()) &&
    typeof body.expectedRevision === "string" && /^generic-action-v2:/.test(body.expectedRevision) &&
    typeof body.actionIntentId === "string" && /^act_\S+$/.test(body.actionIntentId) &&
    typeof body.expectedAuthorityRevision === "string" && /^policy-authority-v1:sha256:[a-f0-9]{64}$/.test(body.expectedAuthorityRevision) &&
    body.preview !== true;
}
