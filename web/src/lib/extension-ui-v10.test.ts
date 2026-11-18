// UI version 10 as the web reads it (docs/extensions/extensions.md "UI version 10", #10060): a tile's emoji, from the
// schema's example, kept as sent beside the icon: the hub checked it (wire.IsEmoji), as it checks the title and
// subtitle the reader also keeps as sent.
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { readView, UI_VERSION, type UIView } from "./extension-ui";

const example = JSON.parse(readFileSync(new URL("../../../schema/extension/examples/View.v10-home.json", import.meta.url), "utf8"));
const tiles = (v: UIView) => v.sections.flatMap((s) => s.items).flatMap((it) => it.tiles ?? []);
const emojiOf = (raw: unknown) => tiles(readView({ sections: [{ items: [{ type: "grid", tiles: [{ title: "t", icon: "light", emoji: raw }] }] }] }))[0];

describe("UI 10: reading a tile's emoji", () => {
  it("is the version the web sends", () => {
    expect(UI_VERSION).toBe(10);
  });

  it("keeps every emoji of the example beside its icon", () => {
    expect(tiles(readView(example)).map((t) => [t.emoji, t.icon])).toEqual([
      ["💡", "light"], ["🔊", "speaker"], ["❄️", "snow"], ["📺", "tv"], ["🌀", "fan"], ["💡", "light"],
    ]);
  });

  it("keeps a joined emoji whole, beside the icon", () => {
    expect(emojiOf("👨‍👩‍👧‍👦")).toMatchObject({ emoji: "👨‍👩‍👧‍👦", icon: "light" });
  });
});
