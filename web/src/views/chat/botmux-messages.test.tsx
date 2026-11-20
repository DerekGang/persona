// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it } from "vitest";
import { setBotmuxMode } from "../../lib/botmux-mode";
import { segments } from "../../lib/turns";
import type { Item, Session } from "../../lib/types";
import { AgentStatusLine } from "./AgentStatusLine";
import { SegmentView } from "./SegmentView";

// A botmux session's exchange as the node sends it (docs/protocol/protocol.md "BotMux messages"): the message with
// botmux's blocks, and the `botmux send` call that answered it in Lark.
const raw = "<botmux_reminder>reply with botmux send</botmux_reminder>\n\n<user_message>\nship it\n</user_message>\n\n<sender type=\"user\" open_id=\"ou_example_alex\" name=\"Alex Chen\" />";
const items: Item[] = [
  { id: "u1#0", seq: 1, role: "user", kind: "text", text: raw, botmux: { kind: "message", text: "ship it", sender: "Alex Chen", sender_type: "user" } },
  { id: "a1#0", seq: 2, role: "assistant", kind: "tool_use", tool_name: "Bash", tool_use_id: "t1", tool_input: { command: "botmux send --response-kind final 'shipped v2'" }, botmux: { kind: "reply", text: "shipped v2", final: true } },
  { id: "r1#0", seq: 3, role: "user", kind: "tool_result", tool_use_id: "t1", text: "{\"ok\":true}" },
];

// the botmux pieces load on demand (React.lazy): render, then let them come in
async function render(): Promise<string> {
  const host = document.createElement("div");
  const root = createRoot(host);
  await act(async () => root.render(<SegmentView seg={segments(items)[0]} live={false} mid="m" sid="s" expandFold={async () => {}} />));
  await act(async () => { await import("./BotmuxMessages"); await new Promise((r) => setTimeout(r, 0)); });
  const html = host.innerHTML;
  act(() => root.unmount());
  return html;
}

afterEach(() => localStorage.clear());

describe("botmux sessions in the chat", () => {
  it("decoded (the default): the words written in Lark, the sender as a tag, and the reply sent there", async () => {
    const html = await render();
    expect(html).toContain('data-botmux-message="message"');
    expect(html).toContain("ship it");
    expect(html).toContain("Alex Chen");
    expect(html).not.toContain("&lt;user_message&gt;");
    expect(html).toContain("data-botmux-original");
    expect(html).toContain("data-botmux-reply");
    expect(html).toContain("shipped v2");
  });

  it("as they are: the transcript's words, no tags, no reply block", async () => {
    setBotmuxMode("all");
    const html = await render();
    expect(html).not.toContain("data-botmux-message");
    expect(html).not.toContain("data-botmux-reply");
    expect(html).toContain("user_message");
  });

  it("shows a one-line composer notice when botmux falls back to external-event trigger", async () => {
    const host = document.createElement("div");
    const root = createRoot(host);
    const session = { machine_id: "m", session_id: "s", title: "t", cwd: "/tmp", status: "idle", botmux_external_trigger: true } as Session;
    await act(async () => root.render(<AgentStatusLine session={session} />));
    expect(host.innerHTML).toContain('data-testid="botmux-external-trigger-note"');
    expect(host.textContent).toContain("3.59.23");
    act(() => root.unmount());
  });

  it("suppresses errored botmux send replies and renders unknown trigger source generically", async () => {
    const erroredItems: Item[] = [
      { id: "u2#0", seq: 1, role: "user", kind: "text", text: "raw", botmux: { kind: "trigger", text: "cron tick", mentions: ["Alice", "Alice"] } },
      { id: "a2#0", seq: 2, role: "assistant", kind: "tool_use", tool_name: "Bash", tool_use_id: "t2", tool_input: { command: "botmux send 'fail'" }, botmux: { kind: "reply", text: "fail" } },
      { id: "r2#0", seq: 3, role: "user", kind: "tool_result", tool_use_id: "t2", is_error: true, text: "error" },
    ];
    const host = document.createElement("div");
    const root = createRoot(host);
    await act(async () => root.render(<SegmentView seg={segments(erroredItems)[0]} live={false} mid="m" sid="s" expandFold={async () => {}} />));
    await act(async () => { await import("./BotmuxMessages"); await new Promise((r) => setTimeout(r, 0)); });
    const html = host.innerHTML;
    expect(html).toContain("外部触发");
    expect(html).not.toContain("从 Persona 发送");
    expect(html).not.toContain("data-botmux-reply");
    act(() => root.unmount());
  });
});
