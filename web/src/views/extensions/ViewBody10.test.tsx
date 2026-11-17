// @vitest-environment jsdom
// UI version 10 as the web draws it (docs/extensions/extensions.md "UI version 10", #9727): a view's `graph` as an
// interactive canvas — the visible nodes as buttons in reading order, the edges in one SVG, the group frames, the
// fold controls, the selected node's ring; a node presses its action, folding is the person's and kept for the
// `key`, a redraw that changes only states keeps the position, a compact card draws the still fitted preview that
// opens Full screen (an interactive canvas of its own), and the keyboard walks the nodes in reading order. Its
// pixels at each width are checked in a real browser (web/e2e/extension-graph.mjs); the layout itself is
// lib/graph-layout.test.ts.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { readView, type UIAction, type UIItem } from "../../lib/extension-ui";
import { ExtensionOwnerScope } from "../../lib/extension-owner";
import { t } from "../../i18n";
import { INERT, ViewSections } from "./ViewBody";
import { forgetGraphStores, GraphCanvas } from "./ViewBody10";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
});

const doc = (html: string) => new DOMParser().parseFromString(`<body>${html}</body>`, "text/html");

// A small work graph: a root with two parts, the first with a grouped pair of ways and a step with a check, the
// second folded with two steps of its own.
const WORK = {
  sections: [{
    title: "Work",
    items: [{ type: "graph", key: "work", nodes: [
      { id: "root", title: "Ship it", subtitle: "2 parts", icon: "list", action: { id: "open", values: { id: "root" } } },
      { id: "a", title: "Part A", group: "Two ways", action: { id: "open", values: { id: "a" } } },
      { id: "b", title: "Part B" },
      { id: "way1", title: "Smallest", group: "Two ways" },
      { id: "way2", title: "Remove", group: "Two ways" },
      { id: "c", title: "Check", tone: "positive", badge: { text: "Done", tone: "positive" }, progress: 0.4 },
      { id: "s1", title: "Step 1", subtitle: "2 suites" },
      { id: "s2", title: "Step 2" },
    ], edges: [
      { from: "root", to: "a" }, { from: "root", to: "b" }, { from: "a", to: "way1" }, { from: "a", to: "way2" },
      { from: "way1", to: "c" }, { from: "b", to: "s1" }, { from: "b", to: "s2" },
    ], folded: ["b"], selected: "c" }],
  }],
};

/** The node ids the canvas shows, in reading order, with `b` folded away. */
const VISIBLE = ["root", "a", "way1", "c", "way2", "b"];

const draw = (view: unknown) => readView(view as object).sections[0].items[0];

let host: HTMLDivElement;
let root: Root;
// the item's key remounts the canvas (a different key is a different graph, as the page's redraws keep it); the owner
// scopes with it, so two owners' graphs never share one instance either — the owner travels as a context
// (lib/extension-owner.tsx), as the surfaces that draw an extension's view provide it
type MountOpts = { compact?: boolean; onPress?: (a: UIAction) => void; owner?: string };
const mount = async (item: UIItem, opts?: MountOpts) => {
  const key = `${opts?.owner ?? ""}/${item.key ?? ""}`;
  const canvas = <GraphCanvas item={item} compact={opts?.compact} busy="" onPress={opts?.onPress ?? (() => {})} />;
  await act(async () =>
    root.render(opts?.owner === undefined ? <GraphCanvas key={key} item={item} compact={opts?.compact} busy="" onPress={opts?.onPress ?? (() => {})} /> : (
      <ExtensionOwnerScope key={key} owner={opts.owner}>{canvas}</ExtensionOwnerScope>
    )),
  );
};

const sizeStage = (w: number, h: number) => {
  const spy = vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockReturnValue(w);
  vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockReturnValue(h);
  return spy;
};

// jsdom has PointerEvent but not the capture API the canvas's drag path reads; a polyfill stands in for the
// browser's (a captured pointer stays the canvas's drag — true once a drag began, exactly what the code asks next).
const capturePolyfill = () => {
  const held = new Set<number>();
  const proto = HTMLElement.prototype as HTMLElement & {
    setPointerCapture: (id: number) => void;
    hasPointerCapture: (id: number) => boolean;
    releasePointerCapture: (id: number) => void;
  };
  proto.setPointerCapture ??= (id: number) => void held.add(id);
  proto.hasPointerCapture ??= (id: number) => held.has(id);
  proto.releasePointerCapture ??= (id: number) => void held.delete(id);
  return held;
};

/** A pointer drag over the stage: down at (x, y), then moves, without a control under it. */
const drag = (stage: Element, moves: [number, number][], pointerId = 1) => {
  const [x0, y0] = moves[0];
  const at = (x: number, y: number) => ({ pointerId, button: 0, clientX: x, clientY: y, bubbles: true, cancelable: true });
  act(() => stage.dispatchEvent(new PointerEvent("pointerdown", at(x0, y0))));
  for (let i = 1; i < moves.length; i++) act(() => stage.dispatchEvent(new PointerEvent("pointermove", at(moves[i][0], moves[i][1]))));
};
/** The layer's transform as numbers: [x, y, zoom]. */
const transformOf = (el: Element | null) =>
  (el as HTMLElement).style.transform.match(/translate\(([-\d.]+)px, ([-\d.]+)px\) scale\(([-\d.]+)\)/)!.slice(1).map(Number);

describe("UI 10: the graph canvas", () => {
  beforeEach(() => {
    forgetGraphStores(); // a test's clean slate: no key keeps another test's pan or folds
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
  });
  afterEach(() => {
    act(() => root.unmount());
    host.remove();
    document.querySelectorAll("[role=dialog]").forEach((d) => d.remove());
  });

  const mountView = async (view: unknown, opts?: { compact?: boolean; onPress?: (a: UIAction) => void; owner?: string }) => {
    const pressed: UIAction[] = [];
    await mount(draw(view), { compact: opts?.compact, onPress: opts?.onPress ?? ((a) => pressed.push(a)), owner: opts?.owner });
    return { pressed };
  };

  it("draws the visible nodes as buttons in reading order, the edges in one SVG behind them", () => {
    // static markup: b is folded, so its own steps are hidden; the rest shows in reading order
    const d = doc(renderToStaticMarkup(<GraphCanvas item={draw(WORK)} busy="" onPress={() => {}} />));
    const nodes = [...d.querySelectorAll("[data-graph-node]")].map((e) => e.getAttribute("data-graph-node"));
    expect(nodes).toEqual(VISIBLE);
    // a node with an action is a button; without one it is a focusable group whose whole label reads out
    const buttons = [...d.querySelectorAll("[data-graph-node]")].filter((e) => e.tagName === "BUTTON");
    expect(buttons.map((b) => b.getAttribute("aria-label"))).toEqual([
      "Ship it，2 parts", // a root: nothing under it
      `Part A，${t("extensions-viewbody.graph_under", { title: "Ship it" })}`,
    ]);
    const bare = d.querySelector('[data-graph-node="c"]')!;
    expect(bare.tagName).not.toBe("BUTTON");
    expect(bare.getAttribute("role")).toBe("group");
    expect(bare.getAttribute("tabindex")).toBe("0"); // Tab reaches every node, with an action or without one
    expect(bare.getAttribute("aria-label")).toBe(`Check，Done，40%，${t("extensions-viewbody.graph_under", { title: "Smallest" })}`);
    expect(d.querySelectorAll("svg path[data-graph-edge]")).toHaveLength(5); // b's two edges are folded away
    expect(d.querySelector("[data-graph-frame]")?.getAttribute("data-graph-frame")).toBe("Two ways");
    expect(d.querySelector("[data-graph-selected]")).not.toBeNull(); // c is the selected node
  });

  it("reads a node out with its badge, subtitle, progress and parent, and a fold as Fold or the hidden count", () => {
    const d = doc(renderToStaticMarkup(<GraphCanvas item={draw(WORK)} busy="" onPress={() => {}} />));
    const c = d.querySelector('[data-graph-node="c"]')!;
    expect(c.getAttribute("aria-label")).toBe(`Check，Done，40%，${t("extensions-viewbody.graph_under", { title: "Smallest" })}`);
    const fold = d.querySelector('[data-graph-fold="b"]')!;
    expect(fold.getAttribute("aria-label")).toBe(t("extensions-viewbody.graph_show_hidden", { count: 2 }));
    expect(fold.textContent).toContain("+2");
    expect(fold.getAttribute("aria-expanded")).toBe("false"); // b is folded: nothing expanded under it
    // an open fold says Fold, expanded
    const rootFold = d.querySelector('[data-graph-fold="root"]')!;
    expect(rootFold.getAttribute("aria-label")).toBe(t("extensions-viewbody.graph_fold"));
    expect(rootFold.getAttribute("aria-expanded")).toBe("true");
  });

  it("puts each fold control inside its node's own listitem, so Tab walks node, its fold, the next node", async () => {
    const d = doc(renderToStaticMarkup(<GraphCanvas item={draw(WORK)} busy="" onPress={() => {}} />));
    const item = d.querySelector('[data-graph-at="root"]')!;
    expect(item.getAttribute("role")).toBe("listitem");
    expect(item.contains(d.querySelector('[data-graph-fold="root"]')!)).toBe(true);
    expect([...d.querySelectorAll("[data-graph-fold]")].every((f) => f.closest('[role="listitem"]') !== null)).toBe(true);
    // and the DOM gives Tab exactly that order: a node, then its own fold, then the next node — jsdom moves no
    // focus itself, but the focusables in document order are what the browser's Tab walks
    await mountView(WORK);
    const list = [...host.querySelectorAll<HTMLElement>("button, [tabindex='0']")];
    const labels = list.map((el) => el.getAttribute("data-graph-node") ?? el.getAttribute("data-graph-fold"));
    expect(labels.filter(Boolean).slice(0, 5)).toEqual(["root", "root", "a", "a", "way1"]); // root's button, root's fold, a's button, a's fold, then way1
  });

  it("presses a node's action and not the fold control beside it", async () => {
    const pressed: UIAction[] = [];
    await mountView(WORK, { onPress: (a) => pressed.push(a) });
    act(() => host.querySelector<HTMLButtonElement>('[data-graph-node="root"]')!.click());
    expect(pressed).toEqual([{ id: "open", values: { id: "root" } }]);
    act(() => host.querySelector<HTMLButtonElement>('[data-graph-fold="b"]')!.click());
    expect(pressed).toHaveLength(1); // folding never presses a node
  });

  it("keeps the node's element and the fold's focus across a redraw and a fold toggle", async () => {
    await mountView(WORK);
    const rootEl = host.querySelector('[data-graph-node="root"]')!;
    // a fold toggle re-renders in place: the node's element stays connected and the fold keeps the focus
    const foldBtn = host.querySelector<HTMLButtonElement>('[data-graph-fold="b"]')!;
    foldBtn.focus();
    act(() => foldBtn.click());
    await act(async () => {});
    expect(host.querySelector('[data-graph-node="root"]')!.isSameNode(rootEl)).toBe(true);
    expect(rootEl.isConnected).toBe(true);
    expect(document.activeElement?.isSameNode(foldBtn)).toBe(true);
    // a redraw that changes only states keeps the same element too
    const again = structuredClone(WORK);
    const nodes = (again.sections[0].items[0] as { nodes: { id: string; badge?: { text: string } }[] }).nodes;
    nodes.find((n) => n.id === "c")!.badge = { text: "Running" };
    await mount(draw(again), { compact: false });
    expect((host.querySelector('[data-graph-node="root"]') as HTMLElement).isSameNode(rootEl)).toBe(true);
    expect(rootEl.isConnected).toBe(true);
  });

  it("folds and unfolds by the person's choice, kept across redraws of the same key", async () => {
    await mountView(WORK);
    expect(host.querySelectorAll("[data-graph-node]")).toHaveLength(VISIBLE.length); // b folded by the view
    act(() => host.querySelector<HTMLButtonElement>('[data-graph-fold="b"]')!.click());
    await act(async () => {}); // the fold's state settles
    expect(host.querySelectorAll("[data-graph-node]")).toHaveLength(VISIBLE.length + 2); // b's steps are back
    // a redraw of the same key with the view's own folded list: the person's choice wins
    const again = structuredClone(WORK);
    (again.sections[0].items[0] as { folded: string[] }).folded = ["b"];
    await mount(draw(again), { compact: false });
    expect(host.querySelectorAll("[data-graph-node]")).toHaveLength(VISIBLE.length + 2); // still the person's: b open
    // a different key takes the view's folds again
    const otherKey = structuredClone(WORK);
    (otherKey.sections[0].items[0] as { key: string }).key = "other";
    await mount(draw(otherKey), { compact: false });
    expect(host.querySelectorAll("[data-graph-node]")).toHaveLength(VISIBLE.length);
  });

  it("gives two keyless graphs their own folds, and keeps two owners' keyed graphs apart", async () => {
    const noKey = structuredClone(WORK);
    delete (noKey.sections[0].items[0] as { key?: string }).key;
    // two keyless graphs, one after another: each takes its own `folded` (b folded, b not)
    await mountView(noKey);
    expect(host.querySelectorAll("[data-graph-node]")).toHaveLength(VISIBLE.length);
    const open = structuredClone(noKey);
    (open.sections[0].items[0] as { folded?: string[] }).folded = [];
    await mount(draw(open), { compact: false });
    expect(host.querySelectorAll("[data-graph-node]")).toHaveLength(VISIBLE.length + 2); // not the first one's folds
    // the same key under two owners: their folds never leak between them
    await mountView(WORK, { owner: "one" });
    act(() => host.querySelector<HTMLButtonElement>('[data-graph-fold="b"]')!.click());
    await act(async () => {});
    await mount(draw(WORK), { owner: "two" });
    expect(host.querySelectorAll("[data-graph-node]")).toHaveLength(VISIBLE.length); // b still folded there
  });

  it("keeps two owners' keyed graphs apart when a lone re-render happens under one of them", async () => {
    // the stale-global shape the reviewer found: two cards on one page, the second card's graph re-renders alone (its
    // own state), and a module global set by the last render would have the first card's scope read the second's
    // owner. A context is right on every re-render, so each owner's entry keeps its own folds.
    const twoHost = document.createElement("div");
    document.body.append(twoHost);
    const twoRoot = createRoot(twoHost);
    try {
      const Card = ({ owner }: { owner: string }) => (
        <ExtensionOwnerScope owner={owner}>
          <GraphCanvas item={draw(structuredClone(WORK))} busy="" onPress={() => {}} />
        </ExtensionOwnerScope>
      );
      await act(async () => twoRoot.render(<><Card key="a" owner="one" /><Card key="b" owner="two" /></>));
      // the person unfolds b under the first owner alone
      const first = twoHost.querySelectorAll("[data-graph-stage]")[0]!;
      act(() => first.querySelector<HTMLButtonElement>('[data-graph-fold="b"]')!.click());
      await act(async () => {});
      expect(twoHost.querySelectorAll("[data-graph-node]")).toHaveLength(2 * VISIBLE.length + 2); // one unfolded, one still folded
      // a re-render of the whole page with the same owners: the first keeps its unfold, the second keeps its fold
      await act(async () => twoRoot.render(<><Card key="a" owner="one" /><Card key="b" owner="two" /></>));
      const per = [...twoHost.querySelectorAll("[data-graph-stage]")].map((s) => s.querySelectorAll("[data-graph-node]").length);
      expect(per).toEqual([VISIBLE.length + 2, VISIBLE.length]);
    } finally {
      act(() => twoRoot.unmount());
      twoHost.remove();
    }
  });

  it("keeps the pan and zoom across a redraw that changes only states, and re-fits while untouched", async () => {
    const spy = sizeStage(900, 500);
    await mountView(WORK);
    const stage = host.querySelector("[data-graph-stage]")!;
    const layer = host.querySelector("[data-graph-layer]") as HTMLElement;
    const before = layer.style.transform;
    expect(before).not.toBe(""); // it laid itself out
    // the person zooms: Ctrl + wheel over the canvas
    act(() => stage.dispatchEvent(new WheelEvent("wheel", { deltaY: -100, ctrlKey: true, bubbles: true, cancelable: true })));
    const moved = layer.style.transform;
    expect(moved).not.toBe(before);
    // a redraw with the same shape but a state moved on: the position stays
    const again = structuredClone(WORK);
    const nodes = (again.sections[0].items[0] as { nodes: { id: string; badge?: { text: string } }[] }).nodes;
    nodes.find((n) => n.id === "c")!.badge = { text: "Running" };
    await mount(draw(again), { compact: false });
    expect((host.querySelector("[data-graph-layer]") as HTMLElement).style.transform).toBe(moved);
    // while the person never moves it, a redraw takes the opening view again
    const otherKey = structuredClone(WORK);
    (otherKey.sections[0].items[0] as { key: string }).key = "fresh";
    await mount(draw(otherKey), { compact: false });
    expect((host.querySelector("[data-graph-layer]") as HTMLElement).style.transform).toBe(before);
    spy.mockRestore();
  });

  it("keeps Fit's view across a redraw of the same key: Fit counts as the person moving the canvas", async () => {
    const spy = sizeStage(900, 500);
    await mountView(WORK);
    const layer = host.querySelector("[data-graph-layer]") as HTMLElement;
    const lw = Number.parseFloat(layer.style.width);
    const lh = Number.parseFloat(layer.style.height);
    // Fit: everything centred at the fitted scale (the opening view would not be centred, with `selected` there)
    act(() => host.querySelector<HTMLButtonElement>(`[aria-label="${t("extensions-viewbody.graph_fit")}"]`)!.click());
    const fitted = transformOf(layer);
    const wantZ = Math.max(0.25, Math.min(1, Math.min((900 - 80) / lw, (500 - 80) / lh)));
    expect(fitted[2]).toBeCloseTo(wantZ, 9);
    expect(fitted[0]).toBeCloseTo((900 - lw * wantZ) / 2, 9);
    // a redraw that changes only states keeps the person's Fit — without marking Fit as a move, the redraw would
    // take the opening view again and throw it away
    const again = structuredClone(WORK);
    const nodes = (again.sections[0].items[0] as { nodes: { id: string; badge?: { text: string } }[] }).nodes;
    nodes.find((n) => n.id === "c")!.badge = { text: "Running" };
    await mount(draw(again), { compact: false });
    expect(transformOf(host.querySelector("[data-graph-layer]"))).toEqual(fitted);
    spy.mockRestore();
  });

  it("a drag keeps panning across a redraw mid-gesture (a live graph's states change while it runs)", async () => {
    capturePolyfill();
    const spy = sizeStage(300, 200); // small enough that the drawing overflows: a pan is not clamped back to centre
    await mountView(WORK);
    const stage = host.querySelector("[data-graph-stage]")!;
    const layer = host.querySelector("[data-graph-layer]") as HTMLElement;
    const at = () => transformOf(layer);
    const start = at();
    drag(stage, [[500, 300], [470, 280]]); // a 30 px pan
    const pan1 = at();
    expect(pan1).not.toEqual(start);
    // a redraw of the same key (states changed) must not lose the pointer: the drag goes on panning
    const again = structuredClone(WORK);
    const nodes = (again.sections[0].items[0] as { nodes: { id: string; badge?: { text: string } }[] }).nodes;
    nodes.find((n) => n.id === "c")!.badge = { text: "Running" };
    await mount(draw(again), { compact: false });
    const stage2 = host.querySelector("[data-graph-stage]")!;
    const layer2 = host.querySelector("[data-graph-layer]") as HTMLElement;
    expect(layer2).toBe(layer); // the redraw re-renders in place, not a remount
    drag(stage2, [[470, 280], [440, 260]], 1); // the same pointer keeps moving
    expect(transformOf(layer2)).not.toEqual(pan1);
    spy.mockRestore();
  });

  it("without a selected node, opens with the first root at the top start", async () => {
    const spy = sizeStage(900, 500);
    const noSel = structuredClone(WORK);
    delete (noSel.sections[0].items[0] as { selected?: string }).selected;
    (noSel.sections[0].items[0] as { key?: string }).key = "fresh-root";
    await mountView(noSel);
    const layer = host.querySelector("[data-graph-layer]") as HTMLElement;
    const lw = Number.parseFloat(layer.style.width);
    const lh = Number.parseFloat(layer.style.height);
    const m = layer.style.transform.match(/translate\(([-\d.]+)px, ([-\d.]+)px\) scale\(([-\d.]+)\)/)!;
    const [x, y, zoom] = m.slice(1).map(Number);
    // a right tree's first root sits halfway down its children: the layer's corner would leave it off-screen. The
    // root's top-left corner (its slot is [0, 0], so at() is MARGIN on both axes) lands at the inset (40, 40) unless
    // an axis fits and clampPan centres it — then the whole drawing is inside, which the numbers say too.
    const rootX = x + 32 * zoom;
    const rootY = y + 32 * zoom;
    if (lw * zoom <= 900 && lh * zoom <= 500) {
      expect(x).toBeCloseTo((900 - lw * zoom) / 2, 9);
      expect(y).toBeCloseTo((500 - lh * zoom) / 2, 9);
    } else {
      expect([rootX, rootY]).toEqual([40, 40]);
    }
    spy.mockRestore();
  });

  it("in a card, draws the still fitted preview that opens Full screen, with no buttons or node presses", async () => {
    const pressed: UIAction[] = [];
    await mountView(WORK, { compact: true, onPress: (a) => pressed.push(a) });
    const preview = host.querySelector(".graph-preview")!;
    expect(preview).not.toBeNull();
    // compact: no zoom buttons, no node presses — the only button is the preview itself
    const labels = [...host.querySelectorAll("button")].map((b) => b.getAttribute("aria-label"));
    expect(labels).toEqual([t("extensions-viewbody.graph_full_screen")]);
    expect(host.querySelector("[data-graph-stage]")!.getAttribute("tabindex")).toBe("-1");
    expect(host.querySelector('[data-graph-node="root"]')!.tagName).not.toBe("BUTTON");
    act(() => (preview as HTMLButtonElement).click());
    await act(async () => {});
    const overlay = document.querySelector("[role=dialog]")!;
    expect(overlay.getAttribute("aria-modal")).toBe("true");
    expect(overlay.querySelector("[data-graph-stage]")).not.toBeNull(); // the same canvas inside
    // Esc closes it (ui/overlay's stack)
    act(() => void window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" })));
    await act(async () => {});
    expect(document.querySelector("[role=dialog]")).toBeNull();
    expect(pressed).toEqual([]); // nothing was pressed on the way
    // the focus comes back to the card's preview button, the control that reopens Full screen — not lost to the body
    expect(document.activeElement?.classList.contains("graph-preview")).toBe(true);
  });

  it("gives the focus back to the Full screen button after Esc, on the page and from a card", async () => {
    // the page: the Full screen button is replaced by the portal while the overlay is over it, so the focus used to
    // fall to the body; it comes back to the button that reopens the overlay
    await mountView(WORK);
    const full = host.querySelector<HTMLButtonElement>(`[aria-label="${t("extensions-viewbody.graph_full_screen")}"]`)!;
    full.focus();
    expect(document.activeElement).toBe(full);
    act(() => full.click());
    await act(async () => {});
    expect(document.querySelector("[role=dialog]")).not.toBeNull();
    act(() => void window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" })));
    await act(async () => {});
    expect(document.querySelector("[role=dialog]")).toBeNull();
    const back = host.querySelector<HTMLButtonElement>(`[aria-label="${t("extensions-viewbody.graph_full_screen")}"]`)!;
    expect(document.activeElement).toBe(back); // a keyboard user presses Enter to reopen, not Tab from the top
    // a card: the preview button is the opener, and it is back too
    await mountView(WORK, { compact: true });
    const preview = host.querySelector<HTMLButtonElement>(".graph-preview")!;
    preview.focus();
    act(() => preview.click());
    await act(async () => {});
    expect(document.querySelector("[role=dialog]")).not.toBeNull();
    act(() => void window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" })));
    await act(async () => {});
    expect(document.activeElement).toBe(host.querySelector<HTMLButtonElement>(".graph-preview"));
  });

  it("a compact preview fits the whole layer into the stage, with no 25% floor", async () => {
    const spy = sizeStage(600, 200);
    await mountView(WORK, { compact: true });
    const layer = host.querySelector("[data-graph-layer]") as HTMLElement;
    // the mock's view is drawn at 600 x 200 (jsdom reports the client sizes the test set): the still preview fits
    // everything into it — zoom = min((w-80)/lw, (h-80)/lh) held to [0.01, 1], centred — never the opening view.
    const lw = Number.parseFloat(layer.style.width);
    const lh = Number.parseFloat(layer.style.height);
    const want = Math.min((600 - 80) / lw, (200 - 80) / lh);
    const m = layer.style.transform.match(/translate\(([-\d.]+)px, ([-\d.]+)px\) scale\(([-\d.]+)\)/)!;
    const [x, y, zoom] = m.slice(1).map(Number);
    expect(zoom).toBeCloseTo(Math.max(0.01, Math.min(1, want)), 9);
    expect(x).toBeCloseTo((600 - lw * zoom) / 2, 9);
    expect(y).toBeCloseTo((200 - lh * zoom) / 2, 9);
    // a re-run of the layout (a redraw of the same item) fits again, not the opening view
    await mount(draw(WORK), { compact: true });
    const again = (host.querySelector("[data-graph-layer]") as HTMLElement).style.transform.match(/translate\(([-\d.]+)px, ([-\d.]+)px\) scale\(([-\d.]+)\)/)!;
    expect(Number(again[3])).toBeCloseTo(Math.max(0.01, Math.min(1, want)), 9);
    spy.mockRestore();
  });

  it("a still card never writes the page's kept transform, and Full screen from a card is interactive", async () => {
    const spy = sizeStage(900, 500);
    const pressed: UIAction[] = [];
    // the page's canvas: the person zooms it (kept for the key)
    await mountView(WORK, { onPress: (a) => pressed.push(a) });
    const stage = host.querySelector("[data-graph-stage]")!;
    act(() => stage.dispatchEvent(new WheelEvent("wheel", { deltaY: -100, ctrlKey: true, bubbles: true, cancelable: true })));
    const kept = (host.querySelector("[data-graph-layer]") as HTMLElement).style.transform;
    expect(kept).not.toBe("");
    // a compact card of the same key fits itself and never touches the kept view; opening it fits again
    await mount(draw(WORK), { compact: true, onPress: (a) => pressed.push(a) });
    const preview = host.querySelector<HTMLButtonElement>(".graph-preview")!;
    expect(preview).not.toBeNull();
    // Full screen from the card: the same interactive canvas — node buttons, fold controls, a drag and a wheel
    act(() => preview.click());
    await act(async () => {});
    const overlay = document.querySelector("[role=dialog]")!;
    const nodeBtn = overlay.querySelector<HTMLButtonElement>('[data-graph-node="root"]')!;
    expect(nodeBtn.tagName).toBe("BUTTON"); // not the still preview's plain card
    expect(overlay.querySelector('[data-graph-fold="root"]')).not.toBeNull();
    act(() => nodeBtn.click());
    expect(pressed).toEqual([{ id: "open", values: { id: "root" } }]);
    // Full screen from the card opens with its own view — the page's opening view when the person has not moved it
    // (the card's fits never touch the store) — never the still preview's own fit.
    const overlayZoom = Number((overlay.querySelector("[data-graph-layer]") as HTMLElement).style.transform.match(/scale\(([-\d.]+)\)/)![1]);
    expect(overlayZoom).toBeGreaterThan(0.5);
    expect(overlayZoom).toBeLessThanOrEqual(1.2); // the opening view's fitted scale held to 50%-100%
    // Esc closes it
    act(() => void window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" })));
    await act(async () => {});
    expect(document.querySelector("[role=dialog]")).toBeNull();
    // the page's own kept view is what it was: the card's fits and the overlay's zoom never reached it
    await mount(draw(WORK), { compact: false });
    const again = (host.querySelector("[data-graph-layer]") as HTMLElement).style.transform.match(/translate\(([-\d.]+)px, ([-\d.]+)px\) scale\(([-\d.]+)\)/)!;
    const keptM = kept.match(/translate\(([-\d.]+)px, ([-\d.]+)px\) scale\(([-\d.]+)\)/)!;
    expect(Number(again[3])).toBeCloseTo(Number(keptM[3]), 9); // the zoom the person left it at
    spy.mockRestore();
  });

  it("walks the nodes with Tab in reading order and presses with Enter", async () => {
    const pressed: UIAction[] = [];
    await mount(draw(WORK), { compact: false, onPress: (a) => pressed.push(a) });
    // every visible node is focusable in the DOM's reading order: a button when it acts, a group when it does not.
    // A button without its own tabindex is focusable by its own nature; a group carries tabindex="0".
    for (const id of VISIBLE) {
      const el = host.querySelector(`[data-graph-node="${id}"]`)!;
      expect(el.tagName === "BUTTON" || el.getAttribute("tabindex") === "0").toBe(true);
      expect(el.getAttribute("tabindex")).not.toBe("-1");
    }
    const order = [...host.querySelectorAll("[data-graph-node]")].map((e) => e.getAttribute("data-graph-node"));
    expect(order).toEqual(VISIBLE);
    const a = host.querySelector<HTMLButtonElement>('[data-graph-node="a"]')!;
    a.focus(); // Tab reaches it in the reading order above
    act(() => a.click()); // Enter or Space presses a focused button (the browser's own button behavior)
    expect(pressed).toEqual([{ id: "open", values: { id: "a" } }]);
  });

  it("grows only through design tokens: no new pixel font sizes or hex colors in what it draws", () => {
    // the per-file ratchet (scripts/ci/web_font_ratchet.py) counts the source; here the markup carries no raw
    // hex color of its own — sizes may still come from shared components (a badge's Pill)
    const html = renderToStaticMarkup(<GraphCanvas item={draw(WORK)} busy="" onPress={() => {}} />);
    expect(html).not.toMatch(/#[0-9a-f]{3,8}\b/i);
  });
});

describe("UI 10: a graph in a view", () => {
  beforeEach(() => {
    forgetGraphStores(); // a test's clean slate: no key keeps another test's pan or folds
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
  });
  afterEach(() => {
    act(() => root.unmount());
    host.remove();
  });

  it("loads the canvas from its own chunk into the page", async () => {
    await act(async () => root.render(<ViewSections view={readView(WORK)} {...INERT} onPress={() => {}} />));
    await vi.waitFor(() => expect(host.querySelectorAll("[data-graph-node]")).toHaveLength(VISIBLE.length), { timeout: 30_000 });
    expect(host.querySelector(".graph-canvas")).not.toBeNull(); // the page keeps its height meanwhile
  }, 60_000);
});
