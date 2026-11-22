// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AgentMessage, AgentSession } from "../../lib/agents";
import { t } from "../../i18n";

const state = vi.hoisted(() => ({
  sends: [] as { id: string; clientId: string; text: string }[],
  actions: [] as string[],
  session: null as AgentSession | null,
}));

const activeSession = (): AgentSession => ({
  id: "bot_external_task:endpoint", source: "bot_external_task", sessionKey: "endpoint", accountId: "external_task",
  provider: "feishu_bot", title: "Synthetic collaborator", kind: "bot", canSend: true, unreadCount: 0, rankTimeMs: 1,
  actions: ["complete", "abandon"], proactive: "unsupported",
});

function history(): AgentMessage[] {
  return state.sends.map((send, i) => ({
    externalId: `public-message-${i + 1}`, sessionKey: "endpoint", role: "user", createTimeMs: Date.now(), position: i + 1,
    blocks: [{ type: "text", text: send.text }],
  }));
}

vi.mock("../../lib/agents", async (original) => ({
  ...(await original<typeof import("../../lib/agents")>()),
  useAgentList: () => ({ sessions: state.session ? [state.session] : [], loaded: true, error: "", hints: [], sources: [] }),
  fetchMessages: async () => ({ session: state.session ?? undefined, messages: history(), hasMore: false }),
  fetchHiddenSessions: async () => [],
  markRead: async () => {},
  sendMessage: async (id: string, clientId: string, text: string) => {
    state.sends.push({ id, clientId, text });
    return { duplicate: false, outbox: { clientId, status: "sent" } };
  },
  sessionAction: async (_id: string, action: string) => {
    state.actions.push(action);
    state.session = { ...activeSession(), canSend: false, actions: [], lifecycle: action === "complete" ? "completed" : "abandoned" };
    return state.session;
  },
  onAgentsChanged: () => () => {},
  refreshAgentSessions: async () => {},
}));
vi.mock("../../lib/store", async (original) => ({
  ...(await original<typeof import("../../lib/store")>()),
  useStore: <T,>(select: (s: { features: string[] }) => T): T => select({ features: [] }),
}));
vi.mock("./PagedSessionList", () => ({ PagedSessionList: () => null }));

import { AgentsView } from "./AgentsView";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let root: Root;
let host: HTMLDivElement;

beforeEach(() => {
  state.sends = [];
  state.actions = [];
  state.session = activeSession();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  document.body.querySelectorAll("[role='menu'], [role='dialog']").forEach((el) => el.remove());
});

async function typeAndSend(text: string) {
  const box = host.querySelector("textarea")!;
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!;
    setter.call(box, text);
    box.dispatchEvent(new Event("input", { bubbles: true }));
  });
  const send = [...host.querySelectorAll("button")].find((b) => b.getAttribute("aria-label") === t("agents-agentsview.send"))!;
  await act(async () => send.click());
}

async function chooseLifecycle(action: "complete" | "abandon") {
  const trigger = [...host.querySelectorAll("button")].find((b) => b.getAttribute("aria-label") === t("agents-agentsview.task_actions"))!;
  await act(async () => trigger.click());
  const startLabel = t(action === "complete" ? "agents-agentsview.complete_task" : "agents-agentsview.abandon_task");
  const start = [...document.querySelectorAll("[role='menuitem']")].find((el) => el.textContent === startLabel)!;
  await act(async () => (start as HTMLElement).click());
  const confirmLabel = t(action === "complete" ? "agents-agentsview.complete_task_confirm" : "agents-agentsview.abandon_task_confirm");
  const confirm = [...document.querySelectorAll("[role='menuitem']")].find((el) => el.textContent?.startsWith(confirmLabel))!;
  await act(async () => (confirm as HTMLElement).click());
}

describe("generic Feishu bot conversation lifecycle", () => {
  it("keeps accepted delivery separate from completion and releases only on the explicit action", async () => {
    await act(async () => root.render(<AgentsView id={state.session!.id} onToggleSidebar={() => {}} />));
    expect(host.querySelector("h1")?.textContent).toBe("Synthetic collaborator");
    expect(host.textContent).toContain(t("lib-agents.feishu_bot"));
    expect(host.textContent).toContain(t("agents-agentsview.proactive_unsupported"));
    expect(host.textContent).not.toContain("oc_scoped");
    expect(host.textContent).not.toContain("ou_external");
    await typeAndSend("First synthetic assignment");
    expect(state.sends).toHaveLength(1);
    expect(state.actions).toEqual([]);
    expect(host.textContent).not.toContain(t("agents-agentsview.lifecycle_completed"));
    await chooseLifecycle("complete");
    expect(state.actions).toEqual(["complete"]);
    expect(host.textContent).toContain(t("agents-agentsview.lifecycle_completed"));
    expect(host.querySelector("textarea")?.disabled).toBe(true);
  });

  it("offers confirmed abandonment through the same generic conversation", async () => {
    await act(async () => root.render(<AgentsView id={state.session!.id} onToggleSidebar={() => {}} />));
    await chooseLifecycle("abandon");
    expect(state.actions).toEqual(["abandon"]);
    expect(host.textContent).toContain(t("agents-agentsview.lifecycle_abandoned"));
    expect(host.querySelector("textarea")?.disabled).toBe(true);
  });
});
