import { describe, expect, it } from "vitest";
import { botmuxReplies, botmuxTags, decodedUser, originalText } from "./botmux";
import type { Step, UserMessage } from "./turns";
import type { Item } from "./types";

const raw = "<botmux_reminder>reply with botmux send</botmux_reminder>\n\n<user_message>\nship it\n</user_message>\n\n<sender type=\"user\" open_id=\"ou_example_alex\" name=\"Alex Chen\" />";
const item: Item = { id: "u1#0", seq: 1, role: "user", kind: "text", text: raw, botmux: { kind: "message", text: "ship it", sender: "Alex Chen", sender_type: "user", quoted: true, attachments: 2, mentions: ["Review Bot"] } };
const msg: UserMessage = { id: "u1#0", text: raw, images: [], contexts: [], rawInput: [item], botmux: item.botmux };

describe("decodedUser", () => {
  it("shows the words written in Lark and keeps the original", () => {
    expect(decodedUser(msg)?.text).toBe("ship it");
    expect(originalText(msg)).toBe(raw);
  });
  it("leaves a message the node did not read alone", () => {
    expect(decodedUser({ ...msg, botmux: undefined })).toBeUndefined();
  });
});

describe("botmuxTags", () => {
  it("names the sender and the facts, mentions last (the tests read zh-Hans)", () => {
    expect(botmuxTags(item.botmux!)).toEqual(["Alex Chen", "引用了消息", "2 个附件", "@Review Bot"]);
  });
  it("says a trigger came from Persona and a reply went to Lark", () => {
    expect(botmuxTags({ kind: "trigger", text: "go", source: "Persona" })).toEqual(["从 Persona 发送"]);
    expect(botmuxTags({ kind: "reply", text: "done", final: true })).toEqual(["飞书最终回复"]);
  });
});

describe("botmuxReplies", () => {
  it("lists the exchange's replies, those folded away included, in order", () => {
    const use: Item = { id: "a1#0", seq: 2, role: "assistant", kind: "tool_use", tool_name: "Bash", tool_use_id: "t1", botmux: { kind: "reply", text: "first" } };
    const plain: Item = { id: "a2#0", seq: 3, role: "assistant", kind: "tool_use", tool_name: "Bash", tool_use_id: "t2" };
    const fold: Item = { id: "fold:a3#0", seq: 4, role: "assistant", kind: "fold", fold: { items: 4, tools: 2, thoughts: 0, through_seq: 6, botmux: [{ kind: "reply", text: "second" }, { kind: "reply", text: "third", final: true }] } };
    const steps: Step[] = [
      { kind: "tool", id: use.id, use, images: [] },
      { kind: "tool", id: plain.id, use: plain, images: [] },
      { kind: "fold", id: fold.id, item: fold },
    ];
    expect(botmuxReplies(steps).map((r) => r.reply.text)).toEqual(["first", "second", "third"]);
  });
});
