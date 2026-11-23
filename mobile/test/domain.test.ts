import { describe, expect, it } from "vitest";
import { agentRows, groupAgents } from "../src/domain/grouping";
import { boldParts, toolName, transcriptRows } from "../src/domain/transcript";
import { resolutionBody } from "../src/runtime/actions";
import { item, SESSIONS } from "./fixtures";

describe("groupAgents", () => {
  it("leaves out the assistant's conversations and hidden rows", () => {
    expect(agentRows(SESSIONS).map((s) => s.session_id).sort()).toEqual(["a", "b", "c", "d", "e", "f"]);
  });

  it("puts what needs you first, then one section per project with worktrees under their repository", () => {
    const sections = groupAgents(SESSIONS);
    expect(sections[0]).toMatchObject({ key: "needs-you", needsYou: true });
    expect(sections[0]!.rows.map((s) => s.session_id).sort()).toEqual(["a", "b"]);
    const rest = sections.slice(1);
    expect(rest).toHaveLength(1);
    expect(rest[0]).toMatchObject({ title: "weather", needsYou: false });
    expect(rest[0]!.rows.map((s) => s.session_id).sort()).toEqual(["c", "d", "e", "f"]);
  });

  it("sorts rows by attention: working before seen", () => {
    const rows = groupAgents(SESSIONS)[1]!.rows.map((s) => s.session_id);
    expect(rows.indexOf("c")).toBeLessThan(rows.indexOf("f"));
  });
});

describe("transcriptRows", () => {
  it("shows messages as bubbles and folds a run of tool calls into one line", () => {
    const rows = transcriptRows([
      item({ id: "1", seq: 1, role: "user", text: "hi" }),
      item({ id: "2", seq: 2, kind: "tool_use", tool_name: "mcp__persona__agents_list" }),
      item({ id: "3", seq: 3, role: "user", kind: "tool_result", text: "6" }),
      item({ id: "4", seq: 4, kind: "tool_use", tool_name: "Bash" }),
      item({ id: "5", seq: 5, text: "  " }),
      item({ id: "6", seq: 6, text: "done" }),
    ]);
    expect(rows.map((r) => r.kind)).toEqual(["message", "tools", "message"]);
    expect(rows[1]).toMatchObject({ names: ["agents_list", "Bash"] });
  });

  it("names a tool without its MCP prefix", () => {
    expect(toolName("mcp__persona__phone_calendar_events")).toBe("phone_calendar_events");
    expect(toolName("Bash")).toBe("Bash");
    expect(toolName(undefined)).toBe("tool");
  });
});

describe("resolutionBody", () => {
  it("builds the hub's answers for a question, a plan and a permission", () => {
    expect(resolutionBody({ kind: "question", answers: { Q: "A" } })).toEqual({ answers: { Q: "A" } });
    expect(resolutionBody({ kind: "plan", approve: true })).toEqual({ approve: true });
    expect(resolutionBody({ kind: "plan", approve: false, feedback: "smaller" })).toEqual({ approve: false, feedback: "smaller" });
    expect(resolutionBody({ kind: "permission", allow: false })).toEqual({ allow: false });
  });
});

describe("boldParts", () => {
  it("splits **bold** runs and keeps the rest", () => {
    expect(boldParts("a **b** c")).toEqual([
      { text: "a ", bold: false },
      { text: "b", bold: true },
      { text: " c", bold: false },
    ]);
    expect(boldParts("plain")).toEqual([{ text: "plain", bold: false }]);
  });
});
