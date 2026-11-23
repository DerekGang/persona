// What the screens ask the hub to do. Reads fill the store; writes carry an op id and show their state at once.
import type { Interaction, Item, Machine, Session } from "@web/lib/types";
import { pair, request, enc, ApiError, uuid, configure, credentials, onSignedOut } from "./client";
import { loadSaved, save } from "./storage";
import { HubSocket } from "./socket";
import { applyItems, changeInteraction, chatKey, getState, interactionsVersion, isLocal, onFrame, patchChat, reset, set } from "./store";

export const sessionPath = (mid: string, sid: string) => `/sessions/${enc(mid)}/${enc(sid)}`;
let socket: HubSocket | undefined;
export const hubSocket = () => socket;

/** Reads what every screen needs: computers, agents, pending questions, and the main chat. */
export async function loadAll(): Promise<void> {
  const ixVersion = interactionsVersion();
  const [machines, sessions, interactions, assistant] = await Promise.allSettled([
    request<{ machines: Machine[] }>("/machines"),
    request<{ sessions: Session[] }>("/sessions"),
    request<{ interactions: Interaction[] }>("/interactions"),
    request<{ main?: string; machine_id?: string }>("/assistant"),
  ]);
  const patch: Parameters<typeof set>[0] = {};
  if (machines.status === "fulfilled") patch.machines = machines.value.machines ?? [];
  if (sessions.status === "fulfilled") {
    patch.sessions = sessions.value.sessions ?? [];
    patch.sessionsLoaded = true;
    patch.sessionsError = undefined;
  } else patch.sessionsError = String(sessions.reason instanceof Error ? sessions.reason.message : sessions.reason);
  // a card that changed while the GET was in flight is newer than the snapshot: keep the live state
  if (interactions.status === "fulfilled" && interactionsVersion() === ixVersion) patch.interactions = interactions.value.interactions ?? [];
  if (assistant.status === "fulfilled" && assistant.value.main && assistant.value.machine_id) patch.main = { mid: assistant.value.machine_id, sid: assistant.value.main };
  set(patch);
}

export async function refetchSessions() {
  try {
    const r = await request<{ sessions: Session[] }>("/sessions");
    set({ sessions: r.sessions ?? [], sessionsLoaded: true, sessionsError: undefined });
  } catch (e) {
    set({ sessionsError: e instanceof Error ? e.message : String(e) });
  }
}

function startSocket() {
  const c = credentials();
  socket?.close();
  if (!c) return;
  socket = new HubSocket(c.base, c.token, {
    onFrame: (f) => {
      onFrame(f, () => void refetchSessions());
      if (f.type === "items") {
        // remember how far each conversation is read, so a reconnect resumes instead of replaying
        const seq = Math.max(0, ...((f.items as Item[] | null) ?? []).map((i) => i.seq || 0));
        if (seq) socket?.noteSeq(f.machine_id as string, f.session_id as string, seq);
      }
    },
    onConn: (conn) => set({ conn }),
    // a revoked token only shows as a socket that never opens: this call's 401 signs the app out
    onStuck: () => void request("/machines").catch(() => {}),
    onHello: (resumed) => {
      if (!resumed) void loadAll();
    },
  });
  socket.connect();
}

/** Starts from what the secure store holds: signed out, or connected. */
export async function boot(preset?: { base: string; token: string }): Promise<void> {
  onSignedOut(() => void signOut());
  const saved = preset ?? (await loadSaved());
  if (!saved) {
    set({ auth: "signedOut" });
    return;
  }
  configure(saved);
  set({ auth: "ready" });
  startSocket();
  await loadAll();
}

export async function connect(base: string, input: string, deviceName: string): Promise<void> {
  const c = await pair(base, input, deviceName);
  await save(c);
  configure(c);
  set({ auth: "ready" });
  startSocket();
  await loadAll();
}

export async function signOut(): Promise<void> {
  socket?.close();
  socket = undefined;
  configure(null);
  await save(null);
  reset();
  set({ auth: "signedOut" });
}

// ── conversations ───────────────────────────────────────

/** Opens a conversation: the last turns over HTTP, then live items over the socket. Returns what to call on leaving. */
export function openChat(mid: string, sid: string): () => void {
  void loadItems(mid, sid);
  socket?.subscribe(mid, sid);
  return () => socket?.unsubscribe(mid, sid);
}

export async function loadItems(mid: string, sid: string): Promise<void> {
  try {
    const r = await request<{ items: Item[]; has_more?: boolean; to_seq?: number }>(`${sessionPath(mid, sid)}/items?turns=2`);
    applyItems(mid, sid, r.items ?? [], r.has_more === true);
    if (r.to_seq) socket?.noteSeq(mid, sid, r.to_seq);
  } catch (e) {
    patchChat(mid, sid, () => ({ loaded: true, error: e instanceof Error ? e.message : String(e) }));
  }
}

export async function loadEarlier(mid: string, sid: string): Promise<void> {
  const first = getState().chats[chatKey(mid, sid)]?.items.find((i) => !isLocal(i));
  if (!first) return;
  try {
    const r = await request<{ items: Item[]; has_more?: boolean }>(`${sessionPath(mid, sid)}/items?before_seq=${first.seq}&turns=2`);
    applyItems(mid, sid, r.items ?? [], r.has_more === true);
  } catch {
    // the button stays: tapping it again retries
  }
}

/** Sends a message: the row shows at once as Sending, becomes Sent when the hub took it, Failed with a retry otherwise. */
export async function sendMessage(mid: string, sid: string, text: string, retryId?: string): Promise<void> {
  const body = text.trim();
  if (!body) return;
  const id = retryId ?? uuid();
  patchChat(mid, sid, (c) => ({
    items: retryId ? c.items : [...c.items, { id: `local:${id}`, seq: 0, role: "user", kind: "text", text: body, client_msg_id: id, ts: new Date().toISOString() }],
    sends: { ...c.sends, [id]: "sending" },
  }));
  try {
    await request(`${sessionPath(mid, sid)}/messages`, { method: "POST", opId: id, json: { text: body, client_msg_id: id } });
    patchChat(mid, sid, (c) => (c.sends[id] === "sending" ? { sends: { ...c.sends, [id]: "sent" } } : {}));
  } catch {
    // the hub may have taken the message and echoed it before the reply was lost: then the local row is gone, nothing failed
    patchChat(mid, sid, (c) => (c.items.some((i) => isLocal(i) && i.client_msg_id === id) ? { sends: { ...c.sends, [id]: "failed" } } : {}));
  }
}

export function retryMessage(mid: string, sid: string, id: string) {
  const row = getState().chats[chatKey(mid, sid)]?.items.find((i) => i.client_msg_id === id && isLocal(i));
  if (row?.text) void sendMessage(mid, sid, row.text, id);
}

// ── questions ───────────────────────────────────────────

export type Answer =
  | { kind: "question"; answers: Record<string, string> }
  | { kind: "plan"; approve: boolean; feedback?: string }
  | { kind: "permission"; allow: boolean };

export function resolutionBody(a: Answer): Record<string, unknown> {
  switch (a.kind) {
    case "question":
      return { answers: a.answers };
    case "plan":
      return a.approve ? { approve: true } : { approve: false, feedback: a.feedback ?? "" };
    case "permission":
      return { allow: a.allow };
  }
}

/** Answers a card. A card the hub says is over already (`already`, or 410) goes away as answered. Returns an error text, or "". */
export async function resolveInteraction(ix: Interaction, a: Answer): Promise<string> {
  try {
    await request(`/interactions/${enc(ix.id)}/resolve`, { method: "POST", json: resolutionBody(a), opId: uuid() });
    changeInteraction(ix.id, null);
    return "";
  } catch (e) {
    if (e instanceof ApiError && e.status === 410) {
      changeInteraction(ix.id, null);
      return "";
    }
    return e instanceof Error ? e.message : String(e);
  }
}
