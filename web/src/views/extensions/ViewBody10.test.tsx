// @vitest-environment jsdom
// UI version 10 as the web draws it (docs/extensions/extensions.md "UI version 10", #10060): a tile's emoji takes the
// icon's place at its corner, large and with no tinted square; a screen reader hears the title, not the emoji; a tile
// without one, or with an empty one, keeps its icon.
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { readView } from "../../lib/extension-ui";
import { INERT, ViewSections } from "./ViewBody";

vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
});

const HOME = {
  sections: [{ title: "客厅", items: [{ type: "grid", key: "room", tiles: [
    { key: "a", title: "灯光1", emoji: "💡", icon: "light", color: "amber", active: true, action: { id: "open:a" } },
    { key: "b", title: "空调", subtitle: "遥控", emoji: "❄️", icon: "snow", action: { id: "open:b" } },
    { key: "c", title: "插座", icon: "plug", action: { id: "open:c" } },
    { key: "e", title: "电视", icon: "tv", emoji: "", action: { id: "open:e" } },
  ] }] }],
};

describe("UI 10: a tile's emoji", () => {
  it("draws the emoji in the icon's place, hidden from screen readers, and keeps the icon of a tile without one", () => {
    const d = new DOMParser().parseFromString(`<body>${renderToStaticMarkup(<ViewSections view={readView(HOME)} {...INERT} />)}</body>`, "text/html");
    const tile = (title: string) => [...d.querySelectorAll("[role=listitem]")].find((li) => li.textContent?.includes(title))!;
    for (const [title, emoji] of [["灯光1", "💡"], ["空调", "❄️"]]) {
      const pic = [...tile(title).querySelectorAll("span[aria-hidden=true]")].find((s) => s.textContent === emoji);
      expect(pic, title).toBeTruthy();
      // large, in its own colors: no tinted square (no inline background) and no line icon beside it
      expect(pic!.className).toContain("text-[28px]");
      expect(pic!.getAttribute("style")).toBeNull();
      expect(tile(title).querySelector("svg")).toBeNull();
    }
    // the plain tile, and one whose emoji is empty, keeps its tinted icon square
    for (const title of ["插座", "电视"]) {
      expect(tile(title).querySelector("span[style] svg"), title).toBeTruthy();
      expect(tile(title).textContent, title).not.toMatch(/\p{Extended_Pictographic}/u);
    }
  });
});
