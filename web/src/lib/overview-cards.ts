// Overview's extension cards as the user arranged them (#9745, docs/web/web.md "Overview's extension cards"): their
// order, the ones hidden, and each one's width, half a row (the default, as they always were) or the whole row. Kept
// on the hub for every device of the user (`overview` of GET / PUT /api/v1/navigation; lib/start-page.ts reads it at
// the hello and hears each push, and sends each change made here) and in this browser as its copy, alone where the hub
// (or its default manager) can't keep it.
//
// Which cards there are is the hub's list (lib/extension-slots.ts, in its order: by extension, then the manifest's).
// A card the arrangement names comes in its place; one it does not name (an extension installed since) after the named
// ones, half a row and shown, in the hub's order; an entry naming a card that is not there now is passed over, and kept
// until the user saves another arrangement (it holds only the cards there are then).
import { useSyncExternalStore } from "react";

const KEY = "overview.cards";

/** How many cards an arrangement holds at most (the hub's limit, server/internal/assistant/navigation.go). */
export const OVERVIEW_MOST = 256;

/** A card's width: half a row (the default) or the whole row. */
export type CardSpan = "half" | "full";

/** One card as the user placed it: the extension's id and the card's (its overview_card's id), hidden or not, its width. */
export type OverviewEntry = { extension: string; id: string; hidden?: boolean; span?: CardSpan };

/** A card as Overview draws it: the hub's card, whether it is hidden, its width. */
export type ArrangedCard<T> = { card: T; key: string; hidden: boolean; span: CardSpan };

// an extension's id and a UI contribution's, as the hub checks them (server/internal/extension ValidID)
const ID = /^[a-z0-9][a-z0-9_-]{0,31}$/;

/** A card's key: `<extension>/<id>`. */
export const cardKey = (c: { extension: string; id: string }) => `${c.extension}/${c.id}`;

/** An arrangement as stored or as the hub sends it (anything else is none); entries of another shape are left out. */
export function readOverview(v: unknown): OverviewEntry[] {
  if (!Array.isArray(v)) return [];
  const seen = new Set<string>();
  const out: OverviewEntry[] = [];
  for (const e of v) {
    if (!e || typeof e !== "object") continue;
    const { extension, id, hidden, span } = e as Record<string, unknown>;
    if (typeof extension !== "string" || typeof id !== "string" || !ID.test(extension) || !ID.test(id)) continue;
    const k = `${extension}/${id}`;
    if (seen.has(k) || out.length >= OVERVIEW_MOST) continue;
    seen.add(k);
    out.push({ extension, id, ...(hidden === true ? { hidden: true } : {}), ...(span === "full" ? { span: "full" as const } : {}) });
  }
  return out;
}

/** The stored arrangement (an unreadable or broken value is none). */
export function parseOverview(raw: string | null): OverviewEntry[] {
  try {
    return raw ? readOverview(JSON.parse(raw)) : [];
  } catch {
    return [];
  }
}

/** The cards in the user's arrangement: the named ones in their place, hidden and wide as chosen; then the others in the
 *  hub's order, half a row and shown. Without an arrangement, the hub's cards as they always were. */
export function arrangeCards<T extends { extension: string; id: string }>(cards: readonly T[], prefs: readonly OverviewEntry[]): ArrangedCard<T>[] {
  const byKey = new Map<string, T>();
  for (const c of cards) if (!byKey.has(cardKey(c))) byKey.set(cardKey(c), c);
  const out: ArrangedCard<T>[] = [];
  const placed = new Set<string>();
  for (const e of prefs) {
    const key = cardKey(e);
    const card = byKey.get(key);
    if (!card || placed.has(key)) continue;
    placed.add(key);
    out.push({ card, key, hidden: e.hidden === true, span: e.span === "full" ? "full" : "half" });
  }
  for (const [key, card] of byKey) if (!placed.has(key)) out.push({ card, key, hidden: false, span: "half" });
  return out;
}

/** Whether the half-row cards go two to a row from a tablet's width: when two or more of them are drawn. A lone one
 *  fills the row until the page has room for two columns of rows (web audit item 76; ExtensionSlots' grid). */
export const halvesPaired = (cards: readonly { hidden: boolean; span: CardSpan }[]) => cards.filter((c) => !c.hidden && c.span === "half").length > 1;

/** The arrangement a change writes: every card there is now, in its place, with its choices (the cards not there now
 *  are left out). */
export function entriesOf(cards: readonly ArrangedCard<{ extension: string; id: string }>[]): OverviewEntry[] {
  return cards.slice(0, OVERVIEW_MOST).map(({ card, hidden, span }) => ({ extension: card.extension, id: card.id, ...(hidden ? { hidden: true } : {}), ...(span === "full" ? { span: "full" as const } : {}) }));
}

/** `list` with the card `key` moved one place earlier (-1) or later (+1). */
export function moveEntry(list: readonly OverviewEntry[], key: string, by: -1 | 1): OverviewEntry[] {
  const i = list.findIndex((e) => cardKey(e) === key);
  const j = i + by;
  if (i < 0 || j < 0 || j >= list.length) return [...list];
  const next = [...list];
  [next[i], next[j]] = [next[j], next[i]];
  return next;
}

/** `list` with the card `key` dropped on the card `target`: it takes the target's place, the cards between move up one. */
export function dropEntry(list: readonly OverviewEntry[], key: string, target: string): OverviewEntry[] {
  const from = list.findIndex((e) => cardKey(e) === key);
  const to = list.findIndex((e) => cardKey(e) === target);
  if (from < 0 || to < 0 || from === to) return [...list];
  const next = list.filter((_, i) => i !== from);
  next.splice(to, 0, list[from]);
  return next;
}

/** `list` with the card `key` hidden or shown, or half a row or the whole row. */
export function changeEntry(list: readonly OverviewEntry[], key: string, change: { hidden?: boolean; span?: CardSpan }): OverviewEntry[] {
  return list.map((e) => {
    if (cardKey(e) !== key) return e;
    const hidden = change.hidden ?? e.hidden === true;
    const span = change.span ?? e.span ?? "half";
    return { extension: e.extension, id: e.id, ...(hidden ? { hidden: true } : {}), ...(span === "full" ? { span: "full" as const } : {}) };
  });
}

// ── the stored arrangement ───────────────────────────────

const listeners = new Set<() => void>();
const NONE: OverviewEntry[] = [];
let cached: { raw: string | null; prefs: OverviewEntry[] } = { raw: null, prefs: NONE };
let volatile: { raw: string | null; prefs: OverviewEntry[] } | null = null;

export function getOverviewPrefs(): OverviewEntry[] {
  if (volatile) return volatile.prefs;
  let raw: string | null = null;
  try {
    raw = localStorage.getItem(KEY);
  } catch {
    // storage blocked: as they always were
  }
  if (raw !== cached.raw) cached = { raw, prefs: parseOverview(raw) };
  return cached.prefs;
}

/** Whether this browser holds an arrangement at all (none: the cards as they always were). */
export const overviewChosen = () => getOverviewPrefs().length > 0;

// The hub's copy is told each change made here (lib/start-page.ts), not the ones it sent (adoptOverview).
let told: ((p: OverviewEntry[]) => void) | null = null;
export const onOverviewSaved = (f: (p: OverviewEntry[]) => void) => void (told = f);

/** Takes the hub's arrangement as this browser's. */
export function adoptOverview(v: unknown) {
  keep(readOverview(v));
}

/** Keeps `list` as the arrangement and sends it to the hub. */
export function saveOverview(list: readonly OverviewEntry[]) {
  const p = readOverview(list);
  keep(p);
  told?.(p);
}

/** Back to the cards as they always were: the hub's order, all shown, half a row. */
export const resetOverview = () => saveOverview(NONE);

function keep(p: OverviewEntry[]) {
  const raw = p.length ? JSON.stringify(p) : null;
  try {
    if (raw !== null) localStorage.setItem(KEY, raw);
    else localStorage.removeItem(KEY);
    volatile = null;
  } catch {
    volatile = { raw, prefs: p }; // storage blocked: the change lasts until the page reloads at most
  }
  cached = { raw, prefs: p.length ? p : NONE };
  listeners.forEach((l) => l());
}

// Whether the hub keeps the arrangement: null until it is known, false where its default manager answered a change of
// it without taking it (a manager from before the field; a hub without the route at all is lib/start-page.ts's).
let kept: boolean | null = null;
export const overviewKept = () => kept;
export function noteOverviewKept(on: boolean) {
  if (kept === on) return;
  kept = on;
  listeners.forEach((l) => l());
}

if (typeof window !== "undefined") {
  // another tab changed it
  window.addEventListener("storage", (e) => {
    if (e.key === KEY || e.key === null) {
      volatile = null;
      listeners.forEach((l) => l());
    }
  });
}

const subscribe = (l: () => void) => {
  listeners.add(l);
  return () => void listeners.delete(l);
};

export const useOverviewPrefs = () => useSyncExternalStore(subscribe, getOverviewPrefs, () => cached.prefs);
export const useOverviewKept = () => useSyncExternalStore(subscribe, overviewKept, overviewKept);
