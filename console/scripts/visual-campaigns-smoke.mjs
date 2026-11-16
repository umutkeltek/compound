import { runVisualCampaignsSelfTest } from "../src/lib/visual-campaigns.selftest.tsx";
console.log(`visual-campaigns smoke ok: ${await runVisualCampaignsSelfTest()} discovery, rendered-state and asset-response checks`);
