// The Health home (#13273) keeps /health/new as an alias and past chats at /health/<id>.
import { describe, expect, it } from "vitest";
import { href, parse } from "../../lib/router";

describe("entering Health", () => {
  it("preserves the home alias and conversation addresses", () => {
    expect(parse("/health/new")).toEqual({ name: "health", fresh: true });
    expect(href({ name: "health", fresh: true })).toBe("/health/new");
    expect(parse("/health")).toEqual({ name: "health", sid: undefined });
    expect(href({ name: "health" })).toBe("/health");
    expect(parse("/health/h1")).toEqual({ name: "health", sid: "h1" });
  });
});
