// Each web module the app imports in place must load under the shims and do its job: a regression here means a web
// change made a shared module depend on the DOM or the web runtime.
import { describe, expect, it } from "vitest";
import { pairToken } from "@web/lib/auth";
import { answerLines } from "@web/lib/answered";
import { ago, byAttention, sessionTitle, stateOf } from "@web/lib/format";
import { repoOf } from "@web/lib/agent-repos";
import { retryDelay, wsQuery, afterHello, NO_CURSOR } from "@web/lib/reconnect";
import { stateCount, stateName } from "@web/lib/state-labels";
import { timeMs } from "@web/lib/times";
import { relativeTime } from "@web/lib/relative-time";
import { setLanguage, t, resolveLanguage } from "../src/shims/i18n";
import { m } from "../src/i18n";
import { QUESTION, session } from "./fixtures";

describe("shared web modules under the mobile shims", () => {
  it("state words and counts come from the web's catalog, in each language", () => {
    setLanguage("en");
    expect(stateName("needs_you")).toBe("Needs you");
    expect(stateCount("needs_you", 2)).toBe("2 need you");
    setLanguage("zh-Hans");
    expect(stateName("needs_you")).toBe("需要你");
    setLanguage("ja");
    expect(stateName("needs_you")).toBe("要対応");
    setLanguage("en");
  });

  it("the mobile strings exist in en, zh-Hans and ja", () => {
    for (const l of ["en", "zh-Hans", "ja"] as const) {
      setLanguage(l);
      expect(m("connect")).not.toBe("mobile.connect");
      expect(m("tool_line", { count: 3 })).toContain("3");
    }
    setLanguage("en");
    expect(resolveLanguage("zh-TW")).toBe("zh-Hans");
    expect(resolveLanguage("fr")).toBe("en");
  });

  it("format, grouping and time helpers", () => {
    expect(stateOf(session({ state: undefined, status: "working" }))).toBe("working");
    expect(sessionTitle(session({ title: "Widget" }))).toBe("Widget");
    expect(repoOf("/Users/me/code/weather/.worktrees/widget")).toMatchObject({ name: "weather", main: false });
    const a = session({ session_id: "a", state: "needs_you" });
    const b = session({ session_id: "b", state: "idle" });
    expect([b, a].sort(byAttention).map((s) => s.session_id)).toEqual(["a", "b"]);
    expect(timeMs("2026-11-23T05:00:00Z")).toBe(Date.parse("2026-11-23T05:00:00Z"));
    expect(ago(new Date(Date.now() - 3 * 3600_000).toISOString())).toMatch(/3/);
    expect(relativeTime(new Date().toISOString())).toBe(t("lib-relative-time.just_now"));
  });

  it("pairToken, reconnect math and answered-card lines", () => {
    expect(pairToken("  jp_abc ")).toBe("jp_abc");
    expect(pairToken("persona://pair?token=jp_xyz&host=h")).toBe("jp_xyz");
    expect(pairToken("https://hub.example.com/?token=jp_q")).toBe("jp_q");
    expect(wsQuery("t", { stream: "S", seq: 3 }, ["f"])).toBe("token=t&event_stream=S&event_seq=3&features=f");
    expect(afterHello(NO_CURSOR, { event_stream: "S", event_seq: 4 })).toEqual({ stream: "S", seq: 4 });
    expect(retryDelay(0, false, 0, 0)).toBe(300);
    expect(answerLines(QUESTION, { answers: { "Which framework?": "XCTest" } })).toEqual([{ label: "Which framework?", answer: "XCTest" }]);
  });
});
