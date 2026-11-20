// @vitest-environment jsdom
import { afterEach, describe, expect, it } from "vitest";
import { BOTMUX_MODE_KEY, getBotmuxMode, nextBotmuxMode, readBotmuxMode, setBotmuxMode } from "./botmux-mode";

afterEach(() => localStorage.clear());

describe("botmux mode", () => {
  it("decodes by default", () => {
    expect(readBotmuxMode()).toBe("decode");
  });
  it("decodes an old hidden mode if the shared setting makes the session visible", () => {
    localStorage.setItem(BOTMUX_MODE_KEY, "hide");
    expect(readBotmuxMode()).toBe("decode");
  });
  it("persists the transcript mode without removing visibility migration input", () => {
    expect([nextBotmuxMode("decode"), nextBotmuxMode("all")]).toEqual(["all", "decode"]);
    localStorage.setItem("persona.agentHideBotmux", "1");
    setBotmuxMode("all");
    expect(getBotmuxMode()).toBe("all");
    expect(localStorage.getItem(BOTMUX_MODE_KEY)).toBe("all");
    expect(localStorage.getItem("persona.agentHideBotmux")).toBe("1");
  });
});
