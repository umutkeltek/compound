import type { ReactNode } from "react";

/**
 * The top of a page that answers one question (Today, Work, Decide, Live):
 * the question as the page title, the answer directly under it in one or two
 * plain sentences, and a quiet right slot (read age, one action). Everything
 * else on the page sits below and is quieter. `name` is the page's own name,
 * kept small above the question so the sidebar label still matches.
 */
export function QuestionHeader({
  name,
  question,
  answer,
  right,
}: {
  name: string;
  question: string;
  answer: ReactNode;
  right?: ReactNode;
}) {
  return (
    <header className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span className="text-sm font-semibold text-faint">{name}</span>
        {right ? <div className="flex min-w-0 flex-wrap items-center gap-2">{right}</div> : null}
      </div>
      <h1 className="text-3xl font-semibold tracking-tight text-strong">{question}</h1>
      <div className="max-w-read text-lg leading-snug text-ink">{answer}</div>
    </header>
  );
}
