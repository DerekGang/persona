// Overview's extension cards as the user arranged them (#9745, docs/web/web.md "Overview's extension cards"): their
// order, the ones hidden, and each one's width, half a row (the default, as they always were) or the whole row. Kept
// on the hub for every device of the user (`overview` of GET / PUT /api/v1/navigation; lib/start-page.ts reads it at
// the hello and hears each push, and sends each change made here) and in this browser as its copy, alone where the hub
// (or its default manager) can't keep it.
//
// Which cards there are is the hub's list (lib/extension-slots.ts, in its order: by extension, then the manifest's).
// A card the arrangement names comes in its place; one it does not name (an extension installed since) after the named
// ones, half a row and shown, in the hub's order; an entry naming a card that is not there now is passed over, and kept
// until the user saves another arrangement (it holds only the cards there are then). Changing it is
// lib/overview-edit.ts's, loaded with Overview's card editor: the first screen only draws the cards.
import { useSyncExternalStore } from "react";

const KEY = "overview.cards";

/** A card's width: half a row (the default) or the whole row. */
export type CardSpan = "half" | "full";

/** One card as the user placed it: the extension's id and the card's (its overview_card's id), hidden or not, its width. */
export type OverviewEntry = { extension: string; id: string; hidden?: boolean; span?: CardSpan };

/** A card as Overview draws it: the hub's card, whether it is hidden, its width. */
export type ArrangedCard<T> = { card: T; key: string; hidden: boolean; span: CardSpan };

/** A card's key: `<extension>/<id>`. */
export const cardKey = (c: { extension: string; id: string }) => `${c.extension}/${c.id}`;

/** An arrangement as stored or as the hub sends it (anything else is none; the hub checked its shape). */
export const readOverview = (v: unknown): OverviewEntry[] =>
  Array.isArray(v) ? v.filter((e) => typeof e?.extension === "string" && typeof e.id === "string") : [];

/** The cards in the user's arrangement: the named ones in their place, hidden and wide as chosen; then the others in the
 *  hub's order, half a row and shown. Without an arrangement, the hub's cards as they always were. */
export function arrangeCards<T extends { extension: string; id: string }>(cards: readonly T[], prefs: readonly OverviewEntry[]): ArrangedCard<T>[] {
  const byKey = new Map<string, T>();
  for (const c of cards) if (!byKey.has(cardKey(c))) byKey.set(cardKey(c), c);
  const out: ArrangedCard<T>[] = [];
  for (const e of prefs) {
    const key = cardKey(e);
    const card = byKey.get(key);
    if (!card) continue; // not there now, or named twice
    byKey.delete(key);
    out.push({ card, key, hidden: e.hidden === true, span: e.span === "full" ? "full" : "half" });
  }
  for (const [key, card] of byKey) out.push({ card, key, hidden: false, span: "half" });
  return out;
}

/** Whether the half-row cards go two to a row from a tablet's width: when two or more of them are drawn (`all`: the
 *  hidden ones too, drawn while arranging). A lone one fills the row until the page has room for two columns of rows
 *  (web audit item 76; ExtensionSlots' grid). */
export const halvesPaired = (cards: readonly { hidden: boolean; span: CardSpan }[], all = false) => cards.filter((c) => (all || !c.hidden) && c.span === "half").length > 1;

// ── the stored arrangement ───────────────────────────────

const listeners = new Set<() => void>();
const changed = () => listeners.forEach((l) => l());
// this browser's copy as read from storage (null: not read yet), the one shown until it changes
let prefs: OverviewEntry[] | null = null;
function read() {
  try {
    return readOverview(JSON.parse(localStorage.getItem(KEY) ?? "[]"));
  } catch {
    return []; // none, broken, or storage blocked: as they always were
  }
}

export const getOverviewPrefs = (): OverviewEntry[] => (prefs ??= read());

/** Whether this browser holds an arrangement at all (none: the cards as they always were). */
export const overviewChosen = () => getOverviewPrefs().length > 0;

// The hub's copy is told each change made here (lib/start-page.ts), not the ones it sent (adoptOverview).
let told: ((p: OverviewEntry[]) => void) | null = null;
export const onOverviewSaved = (f: (p: OverviewEntry[]) => void) => void (told = f);

/** Takes the hub's arrangement as this browser's. */
export function adoptOverview(v: unknown) {
  keep(readOverview(v));
}

/** Keeps `list` as the arrangement and sends it to the hub (none: the cards as they always were). */
export function saveOverview(list: OverviewEntry[]) {
  keep(list);
  told?.(list);
}

function keep(p: OverviewEntry[]) {
  prefs = p;
  try {
    if (p.length) localStorage.setItem(KEY, JSON.stringify(p));
    else localStorage.removeItem(KEY);
  } catch {
    // storage blocked: the change lasts until the page reloads
  }
  changed();
}

// Whether the hub keeps the arrangement: null until it is known, false where its default manager answered a change of
// it without taking it (a manager from before the field; a hub without the route at all is lib/start-page.ts's).
let kept: boolean | null = null;
export const overviewKept = () => kept;
export function noteOverviewKept(on: boolean) {
  if (kept !== on) {
    kept = on;
    changed();
  }
}

if (typeof window !== "undefined") {
  // another tab changed it
  window.addEventListener("storage", (e) => {
    if (e.key === KEY || e.key === null) {
      prefs = read();
      changed();
    }
  });
}

const subscribe = (l: () => void) => {
  listeners.add(l);
  return () => void listeners.delete(l);
};

export const useOverviewPrefs = () => useSyncExternalStore(subscribe, getOverviewPrefs, getOverviewPrefs);
export const useOverviewKept = () => useSyncExternalStore(subscribe, overviewKept, overviewKept);
