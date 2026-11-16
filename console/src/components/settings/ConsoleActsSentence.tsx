"use client";

import { CONSOLE_ACTS_SENTENCE } from "@/components/OperatorConnection";

/**
 * The one sentence on what the console can do from the browser, owned by
 * OperatorConnection. It is rendered from a client component because a
 * server component importing a plain value from a "use client" module gets a
 * client reference, not the text.
 */
export function ConsoleActsSentence() {
  return <>{CONSOLE_ACTS_SENTENCE}</>;
}
