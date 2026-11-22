// @vitest-environment jsdom
// Side-panel directory key identity and bounded navigation stack (lib/panel.ts).
import { act, createElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import {
  canGoBack,
  closePanel,
  directoryPanelKey,
  getPanel,
  getPanelHistory,
  MAX_PANEL_HISTORY,
  openPanel,
  popPanel,
  usePanelCanGoBack,
  type Panel,
} from "./panel";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

describe("directory panel identity", () => {
  it("remounts files and Git for different Agent-free workspace directories", () => {
    const a = { mid: "computer", sid: "", cwd: "/projects/a" };
    const b = { ...a, cwd: "/projects/b" };
    expect(directoryPanelKey(a)).not.toBe(directoryPanelKey(b));
    expect(directoryPanelKey(a)).toBe(directoryPanelKey({ ...a }));
  });
  it("retains the actual machine and session identities without inventing a session ID", () => {
    const p = { mid: "computer", sid: "real-session", cwd: "/projects/a" };
    expect(JSON.parse(directoryPanelKey(p))).toEqual([p.mid, p.sid, p.cwd]);
    expect(directoryPanelKey(p)).not.toBe(directoryPanelKey({ ...p, mid: "other" }));
    expect(directoryPanelKey(p)).not.toBe(directoryPanelKey({ ...p, sid: "another" }));
  });
});

const taskA: Panel = { kind: "task", todo: "todo-1" };
const taskB: Panel = { kind: "task", todo: "todo-2" };
const agentA: Panel = { kind: "agent", mid: "m1", sid: "s1" };
const zipFile: Panel = { kind: "file", mid: "m1", path: "/work/report.zip", cwd: "/work", sid: "s1" };
const reviewPanel: Panel = { kind: "review", mid: "m1", sid: "s1", cwd: "/work", scope: "branch" };
const linkPanel: Panel = { kind: "link", url: "https://example.com/doc" };

describe("side panel navigation stack", () => {
  let aside: HTMLElement;
  let insideBtn: HTMLButtonElement;
  let outsideBtn: HTMLButtonElement;

  beforeEach(() => {
    closePanel();
    aside = document.createElement("aside");
    aside.dataset.sidePanel = "";
    insideBtn = document.createElement("button");
    insideBtn.textContent = "report.zip";
    aside.append(insideBtn);
    outsideBtn = document.createElement("button");
    outsideBtn.textContent = "Other task";
    document.body.append(aside, outsideBtn);
  });

  afterEach(() => {
    closePanel();
    aside.remove();
    outsideBtn.remove();
  });

  it("pushes previous panel when opened by a click inside [data-side-panel], even without moving focus", () => {
    openPanel(taskA);
    expect(canGoBack()).toBe(false);
    expect(getPanelHistory()).toEqual([]);

    // In Safari/iOS and jsdom, clicking a button does not move document.activeElement into the panel.
    outsideBtn.focus();
    insideBtn.addEventListener("click", () => openPanel(zipFile));
    insideBtn.click();

    expect(getPanel()).toEqual(zipFile);
    expect(canGoBack()).toBe(true);
    expect(getPanelHistory()).toEqual([taskA]);
  });

  it("pushes previous panel when triggered from a portaled menu marked [data-side-panel]", () => {
    openPanel(agentA);
    const portal = document.createElement("div");
    portal.setAttribute("role", "menu");
    portal.dataset.sidePanel = "";
    const item = document.createElement("button");
    item.addEventListener("click", () => openPanel(reviewPanel));
    portal.append(item);
    document.body.append(portal);
    try {
      item.click();
      expect(getPanel()).toEqual(reviewPanel);
      expect(getPanelHistory()).toEqual([agentA]);
    } finally {
      portal.remove();
    }
  });

  it("pushes previous panel when document.activeElement is inside [data-side-panel]", () => {
    openPanel(taskA);
    insideBtn.focus();
    openPanel(zipFile);

    expect(getPanel()).toEqual(zipFile);
    expect(getPanelHistory()).toEqual([taskA]);
  });

  it("clears the stack when opened from outside the side panel, even if focus remained inside the panel", () => {
    openPanel(taskA);
    openPanel(zipFile, { push: true });
    expect(getPanelHistory()).toEqual([taskA]);

    // Focus is still inside <aside>, but the click comes from an outside button (main chat).
    insideBtn.focus();
    outsideBtn.addEventListener("click", () => openPanel(taskB));
    outsideBtn.click();

    expect(getPanel()).toEqual(taskB);
    expect(canGoBack()).toBe(false);
    expect(getPanelHistory()).toEqual([]);
  });

  it("supports explicit push: true, push: false, and replace: true options", () => {
    openPanel(taskA);
    openPanel(reviewPanel, { push: true });
    expect(getPanelHistory()).toEqual([taskA]);

    // replace: true swaps current in place without pushing reviewPanel or clearing [taskA].
    openPanel(zipFile, { replace: true });
    expect(getPanel()).toEqual(zipFile);
    expect(getPanelHistory()).toEqual([taskA]);

    // push: false clears the stack even when focus is inside the side panel.
    insideBtn.focus();
    openPanel(linkPanel, { push: false });
    expect(getPanel()).toEqual(linkPanel);
    expect(canGoBack()).toBe(false);
    expect(getPanelHistory()).toEqual([]);
  });

  it("pops panels in LIFO order and closes when the stack is empty", () => {
    openPanel(taskA);
    openPanel(zipFile, { push: true });
    openPanel(linkPanel, { push: true });
    expect(getPanelHistory()).toEqual([taskA, zipFile]);

    expect(popPanel()).toBe(true);
    expect(getPanel()).toEqual(zipFile);
    expect(canGoBack()).toBe(true);

    expect(popPanel()).toBe(true);
    expect(getPanel()).toEqual(taskA);
    expect(canGoBack()).toBe(false);

    // Stack is now empty: popPanel closes the panel and returns false.
    expect(popPanel()).toBe(false);
    expect(getPanel()).toBeNull();
    expect(canGoBack()).toBe(false);

    // Popping when already closed is a safe no-op.
    expect(popPanel()).toBe(false);
    expect(getPanel()).toBeNull();
  });

  it("clears the stack on closePanel()", () => {
    openPanel(taskA);
    openPanel(zipFile, { push: true });
    openPanel(linkPanel, { push: true });
    expect(canGoBack()).toBe(true);

    closePanel();
    expect(getPanel()).toBeNull();
    expect(canGoBack()).toBe(false);
    expect(getPanelHistory()).toEqual([]);
  });

  it("caps history at MAX_PANEL_HISTORY entries, dropping the oldest first", () => {
    openPanel({ kind: "file", mid: "m1", path: "/f0" });
    for (let i = 1; i <= MAX_PANEL_HISTORY + 4; i++) {
      openPanel({ kind: "file", mid: "m1", path: `/f${i}` }, { push: true });
    }
    const hist = getPanelHistory();
    expect(hist).toHaveLength(MAX_PANEL_HISTORY);
    expect(hist[0]).toEqual({ kind: "file", mid: "m1", path: "/f4" });
    expect(hist[hist.length - 1]).toEqual({ kind: "file", mid: "m1", path: `/f${MAX_PANEL_HISTORY + 3}` });
    expect(getPanel()).toEqual({ kind: "file", mid: "m1", path: `/f${MAX_PANEL_HISTORY + 4}` });
  });

  it("suppresses consecutive duplicate pushes (ignoring key order and undefined fields)", () => {
    openPanel(taskA);
    openPanel({ kind: "file", mid: "m1", path: "/work/report.zip", cwd: "/work", sid: "s1" }, { push: true });
    expect(getPanelHistory()).toEqual([taskA]);

    // Opening the exact same file again from inside should not duplicate it on the stack.
    openPanel({ path: "/work/report.zip", kind: "file", mid: "m1", cwd: "/work", sid: "s1", line: undefined }, { push: true });
    expect(getPanelHistory()).toEqual([taskA]);
    expect(getPanel()).toEqual({ path: "/work/report.zip", kind: "file", mid: "m1", cwd: "/work", sid: "s1", line: undefined });
  });

  it("notifies usePanelCanGoBack() reactively", () => {
    const host = document.createElement("div");
    document.body.append(host);
    const root: Root = createRoot(host);
    const Probe = () => createElement("span", { "data-back": String(usePanelCanGoBack()) });
    try {
      act(() => root.render(createElement(Probe)));
      expect(host.querySelector("span")?.getAttribute("data-back")).toBe("false");

      act(() => openPanel(taskA));
      expect(host.querySelector("span")?.getAttribute("data-back")).toBe("false");

      act(() => openPanel(zipFile, { push: true }));
      expect(host.querySelector("span")?.getAttribute("data-back")).toBe("true");

      act(() => { popPanel(); });
      expect(host.querySelector("span")?.getAttribute("data-back")).toBe("false");
    } finally {
      act(() => root.unmount());
      host.remove();
    }
  });
});
