// The hub's live socket: one WebSocket with the web's resume cursor and reconnect backoff (lib/reconnect.ts, in place).
import { afterEvent, afterHello, isResumed, NO_CURSOR, retryDelay, wsQuery, type EventCursor } from "@web/lib/reconnect";

export type Frame = Record<string, unknown> & { type: string };
export type Conn = "connecting" | "online" | "offline";

export const FEATURES = ["sessions_list", "sessions_delta", "session_live"];
const PING_MS = 25_000;

export interface SocketHooks {
  onFrame(f: Frame): void;
  onConn(c: Conn): void;
  /** After every hello; `resumed` means the hub replays what was missed, so nothing needs refetching. */
  onHello(resumed: boolean): void;
  /** The connection keeps failing (every third try): the caller can probe over HTTP, which tells a revoked token (401) from a dead network. */
  onStuck?(): void;
}

type Subscription = { mid: string; sid: string; since?: number };

export class HubSocket {
  private ws?: WebSocket;
  private cursor: EventCursor = NO_CURSOR;
  private tries = 0;
  private timer?: ReturnType<typeof setTimeout>;
  private ping?: ReturnType<typeof setInterval>;
  private closed = false;
  private subs = new Map<string, Subscription>();
  private pings = 0;
  private conn: Conn = "connecting";

  constructor(
    private base: string,
    private token: string,
    private hooks: SocketHooks,
    private make: (url: string) => WebSocket = (u) => new WebSocket(u),
  ) {}

  url(): string {
    const origin = this.base.replace(/^http/i, "ws").replace(/\/+$/, "");
    return `${origin}/api/v1/ws?${wsQuery(this.token, this.cursor, FEATURES)}`;
  }

  connect() {
    this.closed = false;
    this.setConn(this.tries > 1 ? "offline" : "connecting");
    const ws = this.make(this.url());
    this.ws = ws;
    ws.onmessage = (e) => this.message(String(e.data));
    ws.onclose = () => {
      if (this.ws !== ws) return;
      this.ws = undefined;
      clearInterval(this.ping);
      if (this.closed) return;
      this.tries++;
      if (this.tries > 1) this.setConn("offline");
      if (this.tries % 3 === 0) this.hooks.onStuck?.();
      this.timer = setTimeout(() => this.connect(), retryDelay(Math.max(0, this.tries - 1), false, 0, Math.random()));
    };
    ws.onerror = () => {};
  }

  close() {
    this.closed = true;
    clearTimeout(this.timer);
    clearInterval(this.ping);
    this.ws?.close();
    this.ws = undefined;
  }

  send(frame: Record<string, unknown>): boolean {
    if (this.ws?.readyState !== 1) return false;
    this.ws.send(JSON.stringify(frame));
    return true;
  }

  /** Watches a conversation; it is sent again after every reconnect. */
  subscribe(mid: string, sid: string, since?: number) {
    this.subs.set(`${mid}/${sid}`, { mid, sid, since });
    this.sendSub({ mid, sid, since });
  }
  unsubscribe(mid: string, sid: string) {
    this.subs.delete(`${mid}/${sid}`);
    this.send({ type: "unsubscribe", machine_id: mid, session_id: sid });
  }
  /** Remembers how far a conversation has been read, for the resubscribe. */
  noteSeq(mid: string, sid: string, seq: number) {
    const s = this.subs.get(`${mid}/${sid}`);
    if (s && seq > (s.since ?? 0)) s.since = seq;
  }

  private sendSub(s: Subscription) {
    this.send({ type: "subscribe", machine_id: s.mid, session_id: s.sid, ...(s.since ? { since_seq: s.since } : {}), lazy_images: true, fold_work: true });
  }

  private setConn(c: Conn) {
    if (c === this.conn) return;
    this.conn = c;
    this.hooks.onConn(c);
  }

  private message(raw: string) {
    let f: Frame;
    try {
      f = JSON.parse(raw) as Frame;
    } catch {
      return;
    }
    if (typeof f.type !== "string") return;
    this.cursor = f.type === "hello" ? afterHello(this.cursor, f) : afterEvent(this.cursor, f);
    if (f.type === "hello") {
      this.tries = 0;
      this.setConn("online");
      clearInterval(this.ping);
      // a hub with app_ping answers {"type":"ping"} with a pong: a quiet connection is checked, not trusted
      if (Array.isArray(f.features) && f.features.includes("app_ping")) this.ping = setInterval(() => this.send({ type: "ping", id: `mobile-${++this.pings}` }), PING_MS);
      this.subs.forEach((s) => this.sendSub(s));
      this.hooks.onHello(isResumed(f));
    }
    this.hooks.onFrame(f);
  }
}
