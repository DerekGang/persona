import { describe, expect, it, vi } from "vitest";
import { HubSocket, type Frame } from "../src/runtime/socket";

class FakeWS {
  static all: FakeWS[] = [];
  readyState = 1;
  sent: Record<string, unknown>[] = [];
  onmessage?: (e: { data: string }) => void;
  onclose?: () => void;
  onerror?: () => void;
  constructor(public url: string) {
    FakeWS.all.push(this);
  }
  send(s: string) {
    this.sent.push(JSON.parse(s));
  }
  close() {
    this.onclose?.();
  }
  push(f: object) {
    this.onmessage?.({ data: JSON.stringify(f) });
  }
}

function make() {
  FakeWS.all = [];
  const frames: Frame[] = [];
  const conns: string[] = [];
  const hellos: boolean[] = [];
  const s = new HubSocket("http://hub.test", "tok", { onFrame: (f) => frames.push(f), onConn: (c) => conns.push(c), onHello: (r) => hellos.push(r) }, (u) => new FakeWS(u) as unknown as WebSocket);
  return { s, frames, conns, hellos };
}

describe("HubSocket", () => {
  it("connects to the ws route with the token and features, goes online on hello", () => {
    const { s, conns, hellos } = make();
    s.connect();
    const ws = FakeWS.all[0]!;
    expect(ws.url).toMatch(/^ws:\/\/hub\.test\/api\/v1\/ws\?token=tok&features=sessions_list,sessions_delta,session_live$/);
    ws.push({ type: "hello", event_stream: "S1", event_seq: 7, features: [] });
    expect(conns.at(-1)).toBe("online");
    expect(hellos).toEqual([false]);
  });

  it("resumes from the event cursor after a drop, and subscribes again", async () => {
    vi.useFakeTimers();
    const { s, hellos } = make();
    s.connect();
    FakeWS.all[0]!.push({ type: "hello", event_stream: "S1", event_seq: 7 });
    s.subscribe("m1", "s1", 40);
    s.noteSeq("m1", "s1", 55);
    FakeWS.all[0]!.push({ type: "sessions", event_seq: 9, machine_id: "m1", sessions: [] });
    FakeWS.all[0]!.close();
    await vi.advanceTimersByTimeAsync(700);
    const second = FakeWS.all[1]!;
    expect(second.url).toContain("event_stream=S1&event_seq=9");
    second.push({ type: "hello", event_stream: "S1", event_seq: 9, resumed: true });
    expect(second.sent).toContainEqual({ type: "subscribe", machine_id: "m1", session_id: "s1", since_seq: 55, lazy_images: true, fold_work: true });
    expect(hellos).toEqual([false, true]);
    s.close();
    vi.useRealTimers();
  });

  it("says offline after repeated failures and ignores frames without a type", async () => {
    vi.useFakeTimers();
    const { s, conns, frames } = make();
    s.connect();
    FakeWS.all[0]!.onmessage?.({ data: "{}" });
    expect(frames).toHaveLength(0);
    for (let i = 0; i < 3; i++) {
      FakeWS.all.at(-1)!.close();
      await vi.advanceTimersByTimeAsync(20_000);
    }
    expect(conns).toContain("offline");
    s.close();
    vi.useRealTimers();
  });

  it("stays quiet after close()", async () => {
    vi.useFakeTimers();
    const { s } = make();
    s.connect();
    s.close();
    await vi.advanceTimersByTimeAsync(30_000);
    expect(FakeWS.all).toHaveLength(1);
    vi.useRealTimers();
  });
});
