/*
 * Engine — the RENTED layer. Storage + embedding + retrieval live behind this
 * interface so the owned Memory spine never depends on a specific backend.
 * gbrain implements it today; a Postgres+pgvector engine could tomorrow.
 * "Own the spine, rent the executors" — applied to the storage engine itself.
 */

export type Hit = { score: number; slug: string; snippet: string };
export type Fact = { claim: string; kind?: string; source?: string };

export interface Engine {
  /** Human-readable engine id, for provenance/logs. */
  name: string;
  /** Breadth retrieval across the corpus. */
  query(q: string, opts?: { limit?: number }): Promise<Hit[]>;
  /** Write/ingest a markdown page (system-of-record). */
  put(slug: string, markdown: string): Promise<{ ok: boolean; note?: string }>;
  /** Entity-anchored fact recall. */
  recall(entity: string): Promise<Fact[]>;
}
