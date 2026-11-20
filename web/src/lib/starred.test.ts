// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { Star, StarInput } from "./starred-rows";

const io = vi.hoisted(() => ({
  request: vi.fn(),
  signal: new Map<string, () => void>(),
  toast: vi.fn(),
  rows: new Map<string, { id?: string; pending?: "add" | "remove" }>(),
}));

vi.mock("./api", async (original) => ({
  ...await original<typeof import("./api")>(),
  api: io.request,
}));
vi.mock("./store", async (original) => ({
  ...await original<typeof import("./store")>(),
  onHubEvent: (type: string, listener: () => void) => {
    io.signal.set(type, listener);
    return () => io.signal.delete(type);
  },
}));
vi.mock("../ui/atoms", async (original) => ({
  ...await original<typeof import("../ui/atoms")>(),
  toast: io.toast,
}));
vi.mock("./starred-rows", async (original) => ({
  ...await original<typeof import("./starred-rows")>(),
  setStarredRows: (rows: typeof io.rows) => { io.rows = rows; },
}));

import { setPairedToken, setToken } from "./api";
import { chooseManager, MANAGER_HEADER, resetManagerChoice, settleRosterDefault } from "./manager-choice";
import { addStar, removeStar, resetStarredForTest, startStarred } from "./starred";
import { starKey } from "./starred-rows";

const input: StarInput = {
  machine_id: "test-machine", session_id: "test-session", item_id: "test-item",
  conversation: "agent", role: "assistant", title: "Review", speaker: "Agent", text: "A useful reply.",
};
const star: Star = { ...input, id: "saved-star", starred_at: "2026-11-20T00:00:00Z" };
const key = starKey(input);
let refs: Star[] = [];

beforeEach(() => {
  resetManagerChoice();
  setToken("mock");
  settleRosterDefault();
  refs = [];
  io.toast.mockReset();
  io.request.mockReset();
  io.request.mockImplementation(async (_path: string, options: { method?: string }) => {
    if (!options.method) return { refs: refs.slice(), can_write: true };
    throw new Error("Unexpected write");
  });
});
afterEach(() => resetStarredForTest());

it("shows pending immediately, bounds Unicode text, and preserves the prior marker on a failed remove", async () => {
  startStarred();
  let complete: (value: { star: Star }) => void = () => {};
  io.request.mockImplementationOnce((_path, request) => {
    const body = request.json as StarInput;
    expect([...body.text]).toHaveLength(4000);
    expect(body.omitted_chars).toBe(2);
    return new Promise((resolve) => { complete = resolve; });
  });
  const saving = addStar({ ...input, text: "🌱".repeat(4002) });
  expect(io.rows.get(key)).toEqual({ id: undefined, pending: "add" });
  refs = [star];
  complete({ star });
  await saving;
  expect(io.rows.get(key)).toEqual({ id: star.id });
  io.request.mockRejectedValueOnce(new Error("Service unavailable"));
  const removing = removeStar(star);
  expect(io.rows.get(key)).toEqual({ id: star.id, pending: "remove" });
  expect(await removing).toBe(false);
  expect(io.rows.get(key)).toEqual({ id: star.id });
  expect(io.toast.mock.lastCall?.[1]).toBe("error");
});

it("keeps delayed writes and undo in their original assistant and credential generation", async () => {
  refs = [star];
  startStarred();
  await Promise.resolve();
  io.request.mockResolvedValueOnce({ removed: star });
  refs = [];
  expect(await removeStar(star)).toBe(true);
  const undo = io.toast.mock.lastCall?.[2].run as () => void;
  chooseManager("another");
  const requests = io.request.mock.calls.length;
  undo();
  expect(io.request.mock.calls).toHaveLength(requests);

  let complete: (value: { star: Star }) => void = () => {};
  io.request.mockImplementationOnce((_path, opts) => {
    expect(opts.headers[MANAGER_HEADER]).toBe("another");
    return new Promise((resolve) => { complete = resolve; });
  });
  const writing = addStar(input);
  setPairedToken("mock");
  complete({ star });
  await writing;
  expect(io.rows.get(key)).toBeUndefined();
  expect(io.toast).toHaveBeenCalledTimes(1);
});

it("rereads compact references on changes, not full snapshots", async () => {
  startStarred();
  await Promise.resolve();
  refs = [star];
  io.signal.get("assistant_starred")?.();
  await Promise.resolve();
  expect(io.rows.get(key)?.id).toBe(star.id);
  expect(io.request.mock.calls.map(([path]) => path)).toEqual([
    "/assistant/starred?index=1", "/assistant/starred?index=1",
  ]);
});
