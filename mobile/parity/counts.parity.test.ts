import { describe, expect, it } from "vitest";
import { stateCounts } from "../src/domain/counts";
import { stateCounts as webStateCounts } from "@web/lib/sessions";
import { SESSIONS } from "../test/fixtures";

describe("parity with the web's stateCounts (lib/sessions.ts)", () => {
  it("gives the same counts on the same sessions", () => {
    const web = webStateCounts(SESSIONS);
    const { needs, working, waiting, unread, badge } = stateCounts(SESSIONS);
    expect({ needs, working, waiting, unread, badge }).toEqual(web);
  });
});
