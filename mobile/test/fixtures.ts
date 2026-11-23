import type { Interaction, Item, Session } from "@web/lib/types";

export const session = (over: Partial<Session>): Session =>
  ({ machine_id: "m1", session_id: "s", title: "t", cwd: "/Users/me/code/weather", status: "idle", state: "idle", updated_at: "2026-11-23T05:00:00Z", ...over }) as Session;

export const SESSIONS: Session[] = [
  session({ session_id: "a", state: "needs_you", updated_at: "2026-11-23T05:10:00Z" }),
  session({ session_id: "b", state: "needs_you", quiet: true, cwd: "/Users/me/code/photo-sync", machine_id: "m2" }),
  session({ session_id: "c", state: "working" }),
  session({ session_id: "d", state: "working", background_waiting: true }),
  session({ session_id: "e", state: "unread", cwd: "/Users/me/code/weather/.worktrees/widget" }),
  session({ session_id: "f", state: "idle" }),
  session({ session_id: "g", state: "needs_you", hidden_by_directory: true }),
  session({ session_id: "h", state: "idle", assistant: true, cwd: "/home/me/.persona/assistant" }),
];

export const item = (over: Partial<Item>): Item => ({ id: "i", seq: 1, role: "assistant", kind: "text", text: "hi", ...over }) as Item;

export const QUESTION: Interaction = {
  id: "ix1",
  machine_id: "m1",
  session_id: "a",
  kind: "question",
  questions: [{ question: "Which framework?", options: [{ label: "Swift Testing (Recommended)" }, { label: "XCTest" }] }],
} as Interaction;
