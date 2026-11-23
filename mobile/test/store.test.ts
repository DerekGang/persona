import { beforeEach, describe, expect, it } from "vitest";
import { applyItems, applySessionDelta, chatKey, getState, mergeItems, onFrame, reset, set } from "../src/runtime/store";
import { item, QUESTION, session } from "./fixtures";

const noop = () => {};
beforeEach(() => reset());

describe("mergeItems", () => {
  it("orders by seq and replaces an item that arrives again", () => {
    const merged = mergeItems([item({ id: "a", seq: 1, text: "old" }), item({ id: "c", seq: 3 })], [item({ id: "b", seq: 2 }), item({ id: "a", seq: 1, text: "new" })]);
    expect(merged.map((i) => i.id)).toEqual(["a", "b", "c"]);
    expect(merged[0]!.text).toBe("new");
  });

  it("drops the optimistic row when the hub's copy (same client_msg_id) arrives, and keeps local rows last", () => {
    const local = item({ id: "local:x", seq: 0, role: "user", client_msg_id: "x" });
    expect(mergeItems([item({ id: "a", seq: 5 }), local], [item({ id: "z", seq: 6 })]).map((i) => i.id)).toEqual(["a", "z", "local:x"]);
    expect(mergeItems([local], [item({ id: "h#0", seq: 7, role: "user", client_msg_id: "x" })]).map((i) => i.id)).toEqual(["h#0"]);
  });
});

describe("frames", () => {
  it("items frames fill the chat, clear the send state of an echoed message and the live text of a written reply", () => {
    set({ chats: { [chatKey("m", "s")]: { items: [], loaded: false, hasMore: false, live: "par", sends: { x: "sent" } } } });
    onFrame({ type: "items", machine_id: "m", session_id: "s", items: [item({ id: "u", seq: 1, role: "user", client_msg_id: "x" }), item({ id: "r", seq: 2 })] }, noop);
    const c = getState().chats[chatKey("m", "s")]!;
    expect(c.items.map((i) => i.id)).toEqual(["u", "r"]);
    expect(c.sends).toEqual({});
    expect(c.live).toBe("");
    expect(c.loaded).toBe(true);
  });

  it("delta frames build the live reply", () => {
    onFrame({ type: "delta", machine_id: "m", session_id: "s", text: "Hel" }, noop);
    onFrame({ type: "delta", machine_id: "m", session_id: "s", text: "lo" }, noop);
    expect(getState().chats[chatKey("m", "s")]!.live).toBe("Hello");
  });

  it("message_send_state moves only a send it knows, only after a hello that marks receipts, and never back from sent", () => {
    onFrame({ type: "hello", message_send_state: 1 }, noop);
    set({ chats: { [chatKey("m", "s")]: { items: [], loaded: true, hasMore: false, live: "", sends: { x: "sending" } } } });
    onFrame({ type: "message_send_state", machine_id: "m", session_id: "s", client_msg_id: "x", state: "processing" }, noop);
    onFrame({ type: "message_send_state", machine_id: "m", session_id: "s", client_msg_id: "other", state: "failed" }, noop);
    expect(getState().chats[chatKey("m", "s")]!.sends).toEqual({ x: "processing" });
    onFrame({ type: "message_send_state", machine_id: "m", session_id: "s", client_msg_id: "x", state: "delivered" }, noop);
    onFrame({ type: "message_send_state", machine_id: "m", session_id: "s", client_msg_id: "x", state: "processing" }, noop);
    expect(getState().chats[chatKey("m", "s")]!.sends).toEqual({ x: "sent" });
    onFrame({ type: "hello" }, noop); // a hub without receipts: frames are not trusted
    onFrame({ type: "message_send_state", machine_id: "m", session_id: "s", client_msg_id: "x", state: "failed" }, noop);
    expect(getState().chats[chatKey("m", "s")]!.sends).toEqual({ x: "sent" });
  });

  it("any assistant item ends the live reply, so a tools-only turn leaves no stuck bubble", () => {
    onFrame({ type: "delta", machine_id: "m", session_id: "s", text: "par" }, noop);
    onFrame({ type: "items", machine_id: "m", session_id: "s", items: [item({ id: "t", seq: 3, kind: "tool_use" })] }, noop);
    expect(getState().chats[chatKey("m", "s")]!.live).toBe("");
  });

  it("interaction and interaction_resolved add and remove a card", () => {
    onFrame({ type: "interaction", interaction: QUESTION }, noop);
    expect(getState().interactions).toHaveLength(1);
    onFrame({ type: "interaction", interaction: { ...QUESTION, summary: "changed" } }, noop);
    expect(getState().interactions).toHaveLength(1);
    onFrame({ type: "interaction_resolved", id: "ix1" }, noop);
    expect(getState().interactions).toHaveLength(0);
  });

  it("sessions, sessions_delta and session keep the list; sessions_list asks for a refetch", () => {
    let refetched = 0;
    onFrame({ type: "sessions", machine_id: "m1", sessions: [session({ session_id: "a" }), session({ session_id: "b" })] }, noop);
    onFrame({ type: "sessions", machine_id: "m2", sessions: [session({ machine_id: "m2", session_id: "c" })] }, noop);
    onFrame({ type: "sessions_delta", machine_id: "m1", upsert: [session({ session_id: "a", state: "working" })], remove: ["b"] }, noop);
    onFrame({ type: "session", session: session({ session_id: "d" }) }, noop);
    expect(getState().sessions.map((s) => `${s.machine_id}/${s.session_id}`).sort()).toEqual(["m1/a", "m1/d", "m2/c"]);
    expect(getState().sessions.find((s) => s.session_id === "a")!.state).toBe("working");
    onFrame({ type: "sessions_list" }, () => refetched++);
    expect(refetched).toBe(1);
  });

  it("applySessionDelta and applyItems are pure over their inputs", () => {
    const list = [session({ session_id: "a" })];
    expect(applySessionDelta(list, [], ["a"])).toEqual([]);
    expect(list).toHaveLength(1);
    applyItems("m", "s", [item({ id: "1", seq: 1 })], true);
    expect(getState().chats[chatKey("m", "s")]!.hasMore).toBe(true);
  });
});
