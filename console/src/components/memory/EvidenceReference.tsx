import Link from "next/link";
import { evidenceDestination } from "./presentation";

export function EvidenceReference({ reference }: { reference: string }) {
  const destination = evidenceDestination(reference);
  return destination ? (
    <Link href={destination.href} title={reference} className="inline-flex max-w-full flex-wrap items-baseline gap-x-2 text-xs text-work underline-offset-2 hover:underline">
      <span className="font-semibold">{destination.label} →</span>
      <span className="mono break-all text-2xs text-muted">{reference}</span>
    </Link>
  ) : <span className="mono break-all text-2xs text-muted" title={reference}>{reference}</span>;
}
