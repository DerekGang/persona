import { describe, expect, it } from "vitest";
import { decisionCards, decisionDots, decisionKey, decisionPosition, taskDecisionCards } from "./decision-pager";
import { blockedDecisionSkip, decisionSource } from "./decision-details";
import type { Interaction, Item } from "./types";

function question(id: string, extra: Partial<Interaction> = {}): Interaction {
  return { id, machine_id: "machine", session_id: "session", kind: "question", ...extra };
}

describe("assistant decisions", () => {
  it("keeps the conversation's order, sorts the tasks by age, and removes duplicate cards", () => {
    const own = question("own", { created_at: "2026-11-24T10:00:00Z" });
    const cards = decisionCards([own, question("another")], [
      { interaction: question("later", { created_at: "2026-11-24T09:30:00Z" }), taskTitle: "Later" },
      { interaction: own, taskTitle: "Duplicate" },
      { interaction: question("early", { created_at: "2026-11-24T09:00:00Z" }), taskTitle: "Early" },
    ]);
    expect(cards.map(({ interaction }) => interaction.id)).toEqual(["own", "another", "early", "later"]);
    expect(cards[0].taskTitle).toBeUndefined();
    expect(decisionKey(own)).not.toBe(decisionKey({ ...own, manager: "another-manager" }));
  });

  it("keeps the selected ID on arrival and advances from the previous position when answered", () => {
    expect(decisionPosition(["new", "a", "b", "c"], "b", 1)).toBe(2);
    expect(decisionPosition(["a", "c"], "b", 1)).toBe(1);
    expect(decisionPosition(["a"], "b", 1)).toBe(0);
    expect(decisionPosition([], "b", 1)).toBe(-1);
    expect(decisionDots(11, 10)).toEqual([4, 5, 6, 7, 8, 9, 10]);
  });

  it("only projects active task agents in this conversation and manager", () => {
    const time = "2026-11-24T11:00:00Z";
    const todo = {
      id: "task", origin: "assistant", title: "Check the backup", manager: "local",
      created_at: time, updated_at: time,
      agents: [{ machine_id: "machine", session_id: "session", linked_at: time },
        { machine_id: "machine", session_id: "released", linked_at: time, released_at: time }],
    };
    const own = question("task-card", { manager: "local" });
    const differentManager = question("wrong-manager", { manager: "remote" });
    const released = question("old-card", { manager: "local", session_id: "released" });
    expect(taskDecisionCards([todo], [own, differentManager, released], "assistant", "local")).toEqual([{ interaction: own, taskTitle: todo.title }]);
    expect(taskDecisionCards([todo], [own], "another-assistant", "local")).toEqual([]);
    expect(taskDecisionCards([todo], [own], "assistant", "remote")).toEqual([]);
  });

  it("keeps terminal requests and approvals in their existing answer channels", () => {
    expect(blockedDecisionSkip(question("read", { kind: "screen" }))).toBe("terminal");
    expect(blockedDecisionSkip(question("read-with-chat", { kind: "screen", dialog: { options: [{ key: "2", label: "Reply in chat", kind: "chat" }] } }))).toBe("terminal_chat");
    expect(blockedDecisionSkip(question("command", { kind: "screen", dialog: { command: "echo example" } }))).toBe("permission");
    expect(blockedDecisionSkip(question("permit", { kind: "permission" }))).toBe("permission");
    expect(blockedDecisionSkip(question("plan", { kind: "plan" }))).toBe("plan");
    expect(blockedDecisionSkip(question("plain"))).toBeUndefined();
  });

  it("finds an exact tool call, or the newest question for a terminal-read task", () => {
    const items: Item[] = [
      { id: "request", role: "user", kind: "text", seq: 1 },
      { id: "first", role: "assistant", kind: "tool_use", tool_use_id: "exact", tool_name: "AskUserQuestion", seq: 2 },
      { id: "reply", role: "assistant", kind: "text", text: "Here are two more choices.", seq: 3 },
      { id: "last", role: "assistant", kind: "tool_use", tool_use_id: "other", tool_name: "request_user_input", seq: 4 },
    ];
    expect(decisionSource(question("card", { tool_use_id: "exact" }), items, true)).toBe("request");
    expect(decisionSource(question("terminal", { kind: "screen" }), items, true)).toBe("reply");
    expect(decisionSource(question("missing", { tool_use_id: "missing" }), items, true)).toBeUndefined();
  });
});
