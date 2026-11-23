// The demo's client state: one small external store (useSyncExternalStore), fed by the hub's HTTP reads and socket frames.
// The web has lib/store.ts for this; it is bound to the page (localStorage, window, the router), so it is rewritten here small.
import { useSyncExternalStore } from "react";
import type { Interaction, Item, Machine, Session } from "@web/lib/types";
import type { Conn, Frame } from "./socket";

export type SendState = "sending" | "processing" | "sent" | "failed";

export interface ChatState {
  items: Item[];
  loaded: boolean;
  hasMore: boolean;
  error?: string;
  /** The reply being written, from `delta` frames, until its item arrives. */
  live: string;
  sends: Record<string, SendState>;
}

export interface State {
  /** signedOut: no saved hub. ready: a hub is configured (the socket may still be coming up). */
  auth: "loading" | "signedOut" | "ready";
  conn: Conn;
  hubName: string;
  features: string[];
  machines: Machine[];
  sessions: Session[];
  sessionsLoaded: boolean;
  sessionsError?: string;
  interactions: Interaction[];
  main: { mid: string; sid: string } | null;
  chats: Record<string, ChatState>;
}

export const chatKey = (mid: string, sid: string) => `${mid}/${sid}`;
export const emptyChat = (): ChatState => ({ items: [], loaded: false, hasMore: false, live: "", sends: {} });

const initial = (): State => ({
  auth: "loading",
  conn: "connecting",
  hubName: "",
  features: [],
  machines: [],
  sessions: [],
  sessionsLoaded: false,
  interactions: [],
  main: null,
  chats: {},
});

let state: State = initial();
const listeners = new Set<() => void>();

export const getState = () => state;
export function set(patch: Partial<State>) {
  state = { ...state, ...patch };
  listeners.forEach((l) => l());
}
export function reset() {
  state = initial();
  listeners.forEach((l) => l());
}
export function subscribe(l: () => void) {
  listeners.add(l);
  return () => void listeners.delete(l);
}
export function useStore<T>(select: (s: State) => T): T {
  return useSyncExternalStore(subscribe, () => select(state), () => select(state));
}

export function patchChat(mid: string, sid: string, f: (c: ChatState) => Partial<ChatState>) {
  const k = chatKey(mid, sid);
  const cur = state.chats[k] ?? emptyChat();
  set({ chats: { ...state.chats, [k]: { ...cur, ...f(cur) } } });
}

// ── merging ─────────────────────────────────────────────

const LOCAL = "local:";
export const isLocal = (i: Item) => i.id.startsWith(LOCAL);

/** Items by seq. An item that arrives again (same id) replaces its older copy; the hub's copy of a message sent from
 *  here (same client_msg_id) replaces the optimistic row. Local rows sort last. */
export function mergeItems(have: Item[], incoming: Item[]): Item[] {
  const byId = new Map(have.map((i) => [i.id, i] as const));
  const echoed = new Set(incoming.filter((i) => i.client_msg_id && !isLocal(i)).map((i) => i.client_msg_id));
  for (const i of incoming) byId.set(i.id, i);
  const out = [...byId.values()].filter((i) => !(isLocal(i) && i.client_msg_id && echoed.has(i.client_msg_id)));
  return out.sort((a, b) => (isLocal(a) ? Infinity : a.seq) - (isLocal(b) ? Infinity : b.seq));
}

export function applyItems(mid: string, sid: string, items: Item[], hasMore?: boolean) {
  patchChat(mid, sid, (c) => {
    const sends = { ...c.sends };
    for (const i of items) if (i.client_msg_id && sends[i.client_msg_id] && !isLocal(i)) delete sends[i.client_msg_id];
    const wroteText = items.some((i) => i.role === "assistant" && i.kind === "text");
    return { items: mergeItems(c.items, items), loaded: true, hasMore: hasMore ?? c.hasMore, sends, live: wroteText ? "" : c.live, error: undefined };
  });
}

export function applySessionDelta(list: Session[], upsert: Session[], remove: string[]): Session[] {
  const gone = new Set(remove);
  const next = new Map(list.filter((s) => !gone.has(s.session_id)).map((s) => [`${s.machine_id}/${s.session_id}`, s] as const));
  for (const s of upsert) next.set(`${s.machine_id}/${s.session_id}`, s);
  return [...next.values()];
}

export function changeInteraction(id: string, ix: Interaction | null) {
  const rest = state.interactions.filter((i) => i.id !== id);
  set({ interactions: ix ? [...rest, ix] : rest });
}

/** The socket's frames, as the web's store handles them (lib/store.ts onMessage), for what the demo shows. */
export function onFrame(f: Frame, refetchSessions: () => void) {
  switch (f.type) {
    case "hello":
      set({ features: Array.isArray(f.features) ? (f.features as string[]) : [], hubName: typeof f.hub_name === "string" ? f.hub_name : state.hubName });
      break;
    case "machines":
      set({ machines: (f.machines as Machine[] | null) ?? [] });
      break;
    case "sessions": {
      const mid = f.machine_id as string;
      set({ sessions: [...state.sessions.filter((s) => s.machine_id !== mid), ...((f.sessions as Session[] | null) ?? [])] });
      break;
    }
    case "sessions_delta":
      set({ sessions: applySessionDelta(state.sessions, (f.upsert as Session[]) ?? [], (f.remove as string[]) ?? []) });
      break;
    case "session":
      set({ sessions: applySessionDelta(state.sessions, [f.session as Session], []) });
      break;
    case "sessions_list":
      refetchSessions();
      break;
    case "interaction":
      changeInteraction((f.interaction as Interaction).id, f.interaction as Interaction);
      break;
    case "interaction_resolved":
      changeInteraction(f.id as string, null);
      break;
    case "items":
      applyItems(f.machine_id as string, f.session_id as string, (f.items as Item[]) ?? []);
      break;
    case "delta": {
      const mid = f.machine_id as string;
      const sid = f.session_id as string;
      patchChat(mid, sid, (c) => ({ live: c.live + String(f.text ?? "") }));
      break;
    }
    case "message_send_state": {
      const mid = f.machine_id as string;
      const sid = f.session_id as string;
      const id = f.client_msg_id as string;
      const st = f.state;
      if (typeof id !== "string") break;
      patchChat(mid, sid, (c) => {
        if (!(id in c.sends)) return {};
        const next: SendState | undefined = st === "failed" ? "failed" : st === "processing" ? "processing" : st === "delivered" ? "sent" : undefined;
        return next ? { sends: { ...c.sends, [id]: next } } : {};
      });
      break;
    }
  }
}
