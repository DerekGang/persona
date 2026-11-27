// @vitest-environment jsdom
// While the session waits for the owner, its latest turn has no reply because it is not finished, not because none
// came: the chat says neither "No response returned" nor offers Retry, which would send the question again while the
// card still waits (#17296). The wait is the session's `needs_you`, or one of this conversation's requests being open.
// A turn the agent really ended without a reply keeps both, as before.
// The real page against a fake hub (sync/fake-hub.ts), as the sync scenarios run it.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Interaction, Item } from "../../lib/types";
import { FakeSocket, SyncHub, type SyncCase } from "./sync/fake-hub";

vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = (q: string) => ({ matches: /pointer: *fine|hover: *hover/.test(q), addEventListener() {}, removeEventListener() {} });
  g.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} };
  g.IntersectionObserver = class { observe() {} unobserve() {} disconnect() {} takeRecords() { return []; } };
});

vi.mock("../../lib/panel", async (importOriginal) => ({ ...await importOriginal<typeof import("../../lib/panel")>(), usePanel: () => null }));
vi.mock("../assistant/Persona", () => ({ usePersona: () => ({ name: "Persona" }), PersonaAvatar: () => null }));
vi.mock("../assistant/AssistantChat", () => ({
  AssistantFace: () => null,
  CompactionStrip: () => null,
  useAssistantMenu: () => ({ items: [], dialogs: null }),
}));
vi.mock("../assistant/Readiness", () => ({
  useReadinessGate: () => ({ readiness: null, step: null, blocked: undefined, dialog: null, help() {}, confirm() {} }),
  ReadinessCard: () => null,
  ChatOnlyNote: () => null,
  FailureCard: () => null,
}));
vi.mock("../assistant/Tasks", () => ({
  DayLine: () => null,
  FeedLineView: () => null,
  QuickResultCard: () => null,
  RouteChip: () => null,
  AssistantActionBar: () => null,
  TaskDock: () => null,
  TaskQuestions: () => null,
}));

const store = await import("../../lib/store");
const { setToken } = await import("../../lib/api");
const { t } = await import("../../i18n");
const { ChatView } = await import("./ChatView");
const { resetStalePullsForTests } = await import("./useTranscript");
const { loadPages } = await import("../../lib/lazy-page");
await loadPages();
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const MID = "m1";
const SID = "s1";
const question: Item = { id: "q#0", seq: 10, role: "user", kind: "text", text: "Which cloud credits do I get?", ts: "2026-01-02T03:00:00Z" };
// The agent's last step asks the owner a question and waits for the answer: no reply text follows it.
const ask: Item = { id: "t#0", seq: 11, role: "assistant", kind: "tool_use", tool_use_id: "toolu_1", tool_name: "mcp__persona__ask_user", tool_input: { question: "Which account?" }, ts: "2026-01-02T03:00:05Z" };
const card: Interaction = { id: "ix1", machine_id: MID, session_id: SID, kind: "question", created_at: "2026-01-02T03:00:05Z" };

const chat = (state: string, interactions: Interaction[]): SyncCase => ({
  name: `awaiting_${state}`, description: "", chat: { machine_id: MID, session_id: SID }, devices: ["a"],
  start: { hub: { history: "h1", lines: [question, ask], to_seq: ask.seq, interactions, session: { state }, event_stream: "e1", event_seq: 0 } },
  steps: [], end: {},
});

let host: HTMLDivElement;
let root: Root;
let hub: SyncHub;

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "setInterval", "clearInterval", "Date", "requestAnimationFrame", "cancelAnimationFrame"] });
  vi.setSystemTime(new Date("2026-01-02T03:05:00Z"));
  vi.stubGlobal("WebSocket", FakeSocket);
  localStorage.clear();
  setToken("tok");
  host = document.createElement("div");
  document.body.appendChild(host);
});

afterEach(async () => {
  await act(async () => root?.unmount());
  host.remove();
  store.disconnect();
  store.reset();
  resetStalePullsForTests();
  FakeSocket.hub = null;
  vi.unstubAllGlobals();
  vi.useRealTimers();
  localStorage.clear();
});

async function settle(ms = 0) {
  for (let i = 0; i < 4; i++) {
    await act(async () => {
      await vi.advanceTimersByTimeAsync(i === 0 ? ms : 17);
    });
  }
}

async function open(c: SyncCase) {
  hub = new SyncHub(c, "a");
  FakeSocket.hub = hub;
  vi.stubGlobal("fetch", hub.fetch);
  store.connect();
  await settle();
  root = createRoot(host);
  await act(async () => root.render(<ChatView mid={MID} sid={SID} />));
  await settle(100);
  // the empty-response line is a lazy chunk, as the sync scenarios load it
  await act(async () => { await vi.dynamicImportSettled(); });
}

const noReply = () => host.textContent?.includes(t("chat-empty-response.no_response")) ?? false;
const retry = () => [...host.querySelectorAll("button")].find((b) => b.textContent === t("chat-lines.retry"));

describe("a turn the session still waits on", () => {
  it("does not say it has no reply, nor offer Retry, while the session needs the owner", async () => {
    await open(chat("needs_you", []));
    expect(noReply()).toBe(false);
    expect(retry()).toBeUndefined();
  });

  it("does not say it has no reply, nor offer Retry, while one of its requests is open", async () => {
    await open(chat("idle", [card]));
    expect(noReply()).toBe(false);
    expect(retry()).toBeUndefined();
  });

  it("keeps both for a turn that ended without a reply, with nothing left to answer", async () => {
    await open(chat("idle", []));
    expect(noReply()).toBe(true);
    expect(retry()).toBeDefined();
  });
});
