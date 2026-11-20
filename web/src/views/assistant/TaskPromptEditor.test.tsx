// @vitest-environment jsdom
import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { beforeEach, beforeAll, afterEach, expect, it, vi } from "vitest";
import { TaskPromptEditor } from "./TaskPromptEditor";
import { blankTask, type Task } from "../../lib/routines";
import { loadPages } from "../../lib/lazy-page";
import { splitSkillDraft } from "../../lib/skill-mentions";

const api = vi.hoisted(() => vi.fn());
vi.mock("../../lib/api", async (original) => ({ ...(await original<typeof import("../../lib/api")>()), api }));
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let root: Root, host: HTMLDivElement;
const execution = { machine_id: "current", cwd: "/current", launcher: "claude", agent: "claude" };
const catalog = { catalog_version: 1, complete: true, skills: [{ id: "opaque", name: "Index receipts", source: "project" }] };
const draft: Task = { ...blankTask(), prompt: "", cwd: "/current", execution_machine_id: "current" };
const contextPath = "/assistant/tasks/input-context";
const field = () => host.querySelector("textarea")!;
const option = (text: string) => [...host.querySelectorAll('[role="option"]')].find((row) => row.textContent?.includes(text));
// Flush the request debounce and queued React work before reading the next rendered state.
async function settle() {
  await act(async () => { await vi.advanceTimersByTimeAsync(200); });
}
const render = (task: Task) => act(() => root.render(<TaskPromptEditor draft={task} saved={null} disabled={false} onChange={() => {}} />));
function type(text: string) {
  act(() => {
    const el = field();
    el.focus();
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(el, text);
    el.setSelectionRange(text.length, text.length);
    el.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

beforeAll(async () => { await loadPages(); await loadPages(); });
beforeEach(() => {
  vi.useFakeTimers();
  Element.prototype.scrollIntoView ??= () => {};
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  api.mockReset();
});
afterEach(() => { act(() => root.unmount()); host.remove(); vi.useRealTimers(); });

it("ignores an abandoned execution context and requests only the new task directory", async () => {
  let resolveOld!: (value: unknown) => void;
  api.mockImplementation(async (path: string, options) => {
    if (path === contextPath && options.json.cwd === "/old") return new Promise((resolve) => { resolveOld = resolve; });
    if (path === contextPath) return { execution, catalog };
    if (path.includes("/fs?")) return { path: "/current", entries: [{ name: "current.txt", dir: false }], truncated: false };
    return {};
  });
  render({ ...draft, cwd: "/old", execution_machine_id: "old" });
  await settle();
  expect(resolveOld).toBeTypeOf("function");
  render(draft);
  const plus = () => host.querySelector<HTMLButtonElement>("[data-composer-plus]")!;
  await settle();
  expect(plus().disabled).toBe(false);
  await act(async () => resolveOld({ execution: { ...execution, machine_id: "old", cwd: "/old" } }));
  act(() => plus().click());
  await settle();
  expect(option("current.txt")).toBeDefined();
  const files = api.mock.calls.map(([path]) => path as string).filter((path) => path.includes("/fs?"));
  expect(files).toEqual(["/machines/current/fs?path=%2Fcurrent&cwd=%2Fcurrent&task_input=1"]);
});

it("selects a task-scoped slash skill with the keyboard and preserves prose and identity while editing", async () => {
  api.mockImplementation(async (path: string, options) => {
    if (path === contextPath) return { execution, catalog: options.json.catalog ? catalog : undefined };
    return {};
  });
  let raw = "";
  function Form() {
    const [task, setTask] = useState<Task>({ ...draft, repeat: "daily", fresh_each_run: true });
    raw = task.prompt;
    return <TaskPromptEditor draft={task} saved={null} disabled={false}
      onChange={(prompt, prompt_selections) => setTask({ ...task, prompt, prompt_selections })} />;
  }
  act(() => root.render(<Form />));
  await settle();
  expect(host.querySelector<HTMLButtonElement>("[data-composer-plus]")?.disabled).toBe(false);
  type("/Index");
  await settle();
  expect(option("Index receipts")).toBeDefined();
  act(() => { field().dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true })); });
  expect(field().value).toBe("");
  expect(splitSkillDraft(raw, true).skills[0].attrs?.ref).toBe("opaque");
  type("  Review tomorrow\n");
  expect(field().value).toBe("  Review tomorrow\n");
  expect(splitSkillDraft(raw, true).text).toBe(field().value);
  const preview = api.mock.calls.filter(([path]) => path === contextPath);
  expect(preview.map(([, options]) => options.json.catalog ?? false)).toEqual([false, true]);
  expect(preview[1][1].json).toMatchObject({ cwd: "/current", repeat: "daily", fresh_each_run: true });
  expect(preview[1][1].json).not.toHaveProperty("id");
});

it("retains a saved prompt when the execution computer is unavailable", async () => {
  api.mockRejectedValue(new Error("Computer unavailable"));
  const literal = '<persona-context kind="skill" ref="example">Example</persona-context>\n\nKeep my note';
  const task = { ...draft, id: "existing", prompt: literal };
  const changed = vi.fn();
  act(() => root.render(<TaskPromptEditor draft={task} saved={task} disabled={false} onChange={changed} />));
  await settle();
  expect(field().value).toBe(literal);
  expect(host.querySelector("[role=alert]")?.textContent).toContain("Computer unavailable");
  expect(host.querySelector<HTMLButtonElement>("[data-composer-plus]")!.disabled).toBe(true);
  expect(changed).not.toHaveBeenCalled();
});
