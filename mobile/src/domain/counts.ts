// The Agents summary counts. The web's stateCounts (lib/sessions.ts) lives in a module that also holds React hooks and the
// whole store, so it cannot be imported here; this is the same definition, and counts.test.ts holds the two to each
// other with the web's own function on the same fixtures. A candidate for the shared client-runtime package.
import type { Session } from "@web/lib/types";
import { backgroundWaiting, stateOf } from "@web/lib/format";

export interface Counts {
  needs: number;
  working: number;
  /** Working only because its background commands wait: not counted as Working. */
  waiting: number;
  unread: number;
  /** Needs you, without the quiet ones: the number a badge shows. */
  badge: number;
}

export function stateCounts(all: readonly Session[]): Counts {
  const c: Counts = { needs: 0, working: 0, waiting: 0, unread: 0, badge: 0 };
  for (const s of all) {
    if (s.hidden_by_directory) continue;
    const st = stateOf(s);
    if (st === "needs_you") {
      c.needs++;
      if (!s.quiet) c.badge++;
    } else if (st === "working") {
      if (backgroundWaiting(s)) c.waiting++;
      else c.working++;
    } else if (st === "unread") c.unread++;
  }
  return c;
}
