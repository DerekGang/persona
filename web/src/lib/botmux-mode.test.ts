// @vitest-environment jsdom
import { afterEach, describe, expect, it } from "vitest";
import { BOTMUX_MODE_KEY, BOTMUX_VISIBILITY_EVENT, getBotmuxMode, nextBotmuxMode, readBotmuxMode, setBotmuxMode } from "./botmux-mode";

afterEach(() => localStorage.clear());

describe("botmux mode", () => {
  it("decodes by default", () => {
    expect(readBotmuxMode()).toBe("decode");
  });
  it("reads the chip's older hidden value as hide", () => {
    localStorage.setItem("persona.agentHideBotmux", "1");
    expect(readBotmuxMode()).toBe("hide");
    localStorage.setItem("persona.agentHideBotmux", "0");
    expect(readBotmuxMode()).toBe("decode");
  });
  it("cycles decode → as they are → hidden, and stores the choice in place of the old value", () => {
    expect([nextBotmuxMode("decode"), nextBotmuxMode("all"), nextBotmuxMode("hide")]).toEqual(["all", "hide", "decode"]);
    localStorage.setItem("persona.agentHideBotmux", "1");
    setBotmuxMode("all");
    expect(getBotmuxMode()).toBe("all");
    expect(localStorage.getItem(BOTMUX_MODE_KEY)).toBe("all");
    expect(localStorage.getItem("persona.agentHideBotmux")).toBeNull();
  });
  it("tells views outside the store (session history) when the mode changes", () => {
    let fired = 0;
    const count = () => void fired++;
    window.addEventListener(BOTMUX_VISIBILITY_EVENT, count);
    setBotmuxMode("hide");
    setBotmuxMode("hide");
    window.removeEventListener(BOTMUX_VISIBILITY_EVENT, count);
    expect(fired).toBe(1);
  });
});
