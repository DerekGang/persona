// @vitest-environment jsdom
// ChatSearch can take controlled results, a matcher and earlier-load state without calling the default matcher.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ChatSearchHit, ChatSearchResults } from "../../lib/chat-search";
import type { Item } from "../../lib/types";
import type { ChatSearchProps, LoadOutcome } from "./ChatSearch";

const mocks = vi.hoisted(() => ({ searchChat: vi.fn() }));

vi.mock("../../lib/chat-search-match", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../lib/chat-search-match")>()),
  searchChat: mocks.searchChat,
}));

const { ChatSearch } = await import("./ChatSearch");
const { t } = await import("../../i18n");
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const items: Item[] = [
  { id: "u#0", seq: 1, role: "user", kind: "text", text: "look at the parser" },
  { id: "a#0", seq: 2, role: "assistant", kind: "text", text: "the parser is fixed" },
];
const hit: ChatSearchHit = { itemId: "a#0", category: "text", snippet: "the parser is fixed", ranges: [[4, 10]] };
const found: ChatSearchResults = { hits: [hit], truncated: false };

let host: HTMLDivElement;
let root: Root;

const base = (over: Partial<ChatSearchProps> = {}): ChatSearchProps => ({
  open: true,
  query: "",
  onQuery: () => {},
  items,
  rounds: 1,
  hasMore: false,
  onLoadEarlier: async () => null,
  onPick: () => {},
  onClose: () => {},
  ...over,
});

const mount = async (props: ChatSearchProps) => {
  await act(async () => root.render(<ChatSearch {...props} />));
};

const button = (label: string) => {
  const found = [...document.body.querySelectorAll<HTMLButtonElement>("button")].find((item) => item.textContent === label);
  if (!found) throw new Error(`button not found: ${label}`);
  return found;
};

beforeEach(() => {
  vi.clearAllMocks();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  document.body.innerHTML = "";
  vi.useRealTimers();
});

describe("ChatSearch with controlled results and matching", () => {
  it("renders supplied results without calling searchChat or a supplied matcher", async () => {
    const onPick = vi.fn();
    const searchItems = vi.fn(() => found);
    await mount(base({ query: "parser", results: found, searching: true, searchItems, onPick }));

    expect(mocks.searchChat).not.toHaveBeenCalled();
    expect(searchItems).not.toHaveBeenCalled();
    expect(document.body.textContent).toContain("the parser is fixed");
    expect(document.querySelectorAll("[data-search-result]")).toHaveLength(1);
    act(() => document.querySelector<HTMLButtonElement>("[data-search-result]")!.click());
    expect(onPick).toHaveBeenCalledWith(hit);
  });

  it("uses a custom matcher after the configured settle time, not the default matcher", async () => {
    vi.useFakeTimers();
    const searchItems = vi.fn(() => found);
    await mount(base({ searchItems, settleMs: 75 }));
    searchItems.mockClear();
    await mount(base({ query: "parser", searchItems, settleMs: 75 }));
    act(() => vi.advanceTimersByTime(74));
    expect(searchItems).not.toHaveBeenCalled();
    act(() => vi.advanceTimersByTime(1));
    expect(searchItems).toHaveBeenCalledWith(items, "parser");
    expect(mocks.searchChat).not.toHaveBeenCalled();
    expect(document.body.textContent).toContain("the parser is fixed");
  });

  it("uses controlled earlier-load state and stop callback", async () => {
    let stopped: (() => boolean) | undefined;
    const onLoadEarlier = vi.fn(async (_onPage: (n: number) => void, isStopped: () => boolean): Promise<LoadOutcome> => {
      stopped = isStopped;
      return null;
    });
    const onStopLoadEarlier = vi.fn();
    const controlled = {
      query: "parser",
      results: found,
      hasMore: true,
      onLoadEarlier,
      onStopLoadEarlier,
      loadingEarlier: false,
      loadedPages: 4,
      notice: "External stop reason",
    };
    await mount(base(controlled));
    expect(document.body.textContent).toContain("External stop reason");
    await act(async () => button(t("chat-chatsearch.load_earlier")).click());
    expect(onLoadEarlier).toHaveBeenCalledTimes(1);
    expect(stopped?.()).toBe(false);

    await mount(base({ ...controlled, loadingEarlier: true, loadedPages: 6 }));
    expect(document.body.textContent).toContain(t("chat-chatsearch.loading_pages", { count: 6 }));
    act(() => button(t("chat-chatsearch.stop_loading")).click());
    expect(stopped?.()).toBe(true);
    expect(onStopLoadEarlier).toHaveBeenCalledTimes(1);
  });
});

describe("ChatSearch default compatibility", () => {
  it("keeps the 200 ms debounce and default searchChat matcher", async () => {
    vi.useFakeTimers();
    mocks.searchChat.mockImplementation((_items: Item[], query: string) => query ? found : { hits: [], truncated: false });
    await mount(base());
    mocks.searchChat.mockClear();
    await mount(base({ query: "parser" }));
    act(() => vi.advanceTimersByTime(199));
    expect(mocks.searchChat).not.toHaveBeenCalled();
    act(() => vi.advanceTimersByTime(1));
    expect(mocks.searchChat).toHaveBeenCalledWith(items, "parser");
    expect(document.body.textContent).toContain("the parser is fixed");
  });

  it("keeps the earlier-load page and stop lifecycle", async () => {
    mocks.searchChat.mockReturnValue(found);
    let finish: (outcome: LoadOutcome) => void = () => {};
    const onLoadEarlier = vi.fn((onPage: (n: number) => void, _stopped: () => boolean) => {
      onPage(2);
      return new Promise<LoadOutcome>((resolve) => {
        finish = resolve;
      });
    });
    await mount(base({ query: "parser", hasMore: true, onLoadEarlier }));
    await act(async () => button(t("chat-chatsearch.load_earlier")).click());
    expect(document.body.textContent).toContain(t("chat-chatsearch.loading_pages", { count: 2 }));
    act(() => button(t("chat-chatsearch.stop_loading")).click());
    expect(onLoadEarlier.mock.calls[0][1]()).toBe(true);
    await act(async () => finish("Stopped by the reader"));
    expect(document.body.textContent).toContain("Stopped by the reader");
  });
});
