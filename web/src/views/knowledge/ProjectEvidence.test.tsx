// @vitest-environment jsdom
import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { initI18n } from "../../i18n";
import { setToken } from "../../lib/api";
import type { KBProjectKnowledge } from "../../lib/kb";
import { resetManagerChoice, setRosterDefault } from "../../lib/manager-choice";
import { lists, searches } from "./cache";
import { EntryDetail } from "./KBDetail";
import { ListPane } from "./KBList";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
const initial = (): KBProjectKnowledge => ({
  policy: "managed",
  needs_review: true,
  position: 3,
  fields: {
    summary: { status: "needs_review", cutoff: 0, uncovered: 3, locked: true, source_refs: ["chats/sample"] },
    one_line: { status: "current", cutoff: 3, uncovered: 0, writer: "pipeline" },
  },
});
const entry = { kind: "projects", id: "sample", name: "Sample project", one_line: "The changes merged.", relevance: 0, extra: "short · active" };
const metadata = { ...entry, kind: "short", status: "active", pinned: "A historical correction", source_refs: ["https://example.org/project"] };
let knowledge: ReturnType<typeof initial> | undefined;
let refs: string[];
let writes: { body: Record<string, unknown>; manager: string | null }[];
let rejectSave: boolean;
let host: HTMLDivElement;
let root: Root;
const noop = () => {};
function Page({ list = false }: { list?: boolean }) {
  const [reloadKey, setReloadKey] = useState(0);
  return list ? <ListPane kind="projects" detailOpen={false} reloadKey={reloadKey} onOpen={noop} onToggleSidebar={noop} /> :
    <EntryDetail kind="projects" id="sample" reloadKey={reloadKey} onToggleSidebar={noop} onOpen={noop} onBack={noop} onSaved={() => setReloadKey(k => k + 1)} />;
}
const button = (name: string) => Array.from(host.querySelectorAll<HTMLButtonElement>("button")).find(b => b.textContent?.trim() === name);
const openEvidence = () => {
  const summary = Array.from(host.querySelectorAll("summary")).find(el => el.textContent?.includes("Sources and coverage"));
  expect(summary).toBeDefined();
  act(() => summary!.click());
  return summary!.closest("details")!;
};
const click = async (el?: HTMLElement | null) => { expect(el).toBeTruthy(); await act(async () => el!.click()); };
beforeEach(async () => {
  await initI18n("en");
  localStorage.clear();
  resetManagerChoice();
  setToken("synthetic-owner");
  setRosterDefault("sample-manager");
  lists.clear();
  searches.clear();
  knowledge = initial();
  refs = [...metadata.source_refs];
  writes = [];
  rejectSave = false;
  vi.stubGlobal("fetch", vi.fn(async (url: string, init: RequestInit) => {
    let result: unknown = {};
    if (url === "/api/v1/kb/projects") result = { items: [{ ...entry, project_knowledge: knowledge }] };
    if (url === "/api/v1/kb/projects/sample") {
      if (init.method === "PUT") {
        const body = JSON.parse(String(init.body));
        writes.push({ body, manager: new Headers(init.headers).get("X-Persona-Manager") });
        if (rejectSave) return Response.json({ error: "Pointer is invalid" }, { status: 400 });
        if (body.storage_policy && knowledge) knowledge = { ...knowledge, policy: body.storage_policy };
        if (body.source_refs) refs = body.source_refs;
      } else {
        result = { detail: { kind: "projects", id: "sample", path: "", summary: "Two items still pending.", meta: { ...metadata, source_refs: refs }, links: [{ kind: "chats", id: "sample", name: "Change request", relevance: 0 }], project_knowledge: knowledge && { ...knowledge, sources: [{ ref: "chats/sample", state: "merged" }, { ref: "projects/sample/log" }, ...refs.map(ref => ({ ref }))] } } };
      }
    }
    return Response.json(result);
  }));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  resetManagerChoice();
  vi.unstubAllGlobals();
});
it("shows the review gap beside the project in the list", async () => {
  await act(async () => root.render(<Page list />));
  const row = host.querySelector('a[href*="/kb/projects/sample"]');
  expect(row?.textContent).toContain("Summary needs review");
  expect(row?.textContent).toContain("3 newer updates");
  expect(row?.textContent).not.toContain("Overview needs review");
});
it("shows the same gap on the detail and discloses coverage, locks, unknown writer and source state", async () => {
  await act(async () => root.render(<Page />));
  expect(host.textContent).toContain("Summary needs review");
  expect(host.textContent).toContain("Two items still pending.");
  expect(host.querySelector("details")?.open).toBe(false);
  const detail = openEvidence();
  expect(detail.open).toBe(true);
  for (const value of ["Covers 0 of 3 updates", "Covers 3 of 3 updates", "Locked", "Writer unknown", "Persona", "Source status: Merged"]) expect(detail.textContent).toContain(value);
  expect(detail.querySelector('a[href*="/kb/chats/sample"]')?.textContent).toBe("Change request");
  expect(detail.textContent).toContain("projects/sample/log");
  expect(Array.from(detail.querySelectorAll("a")).some(a => a.textContent === "projects/sample/log")).toBe(false);
  expect(writes).toHaveLength(0);
});
it("writes only the changed policy, preserves existing history, and can return to managed storage", async () => {
  await act(async () => root.render(<Page />));
  openEvidence();
  await click(button("Edit storage"));
  await click(host.querySelector<HTMLButtonElement>('[role="switch"]'));
  expect(writes).toHaveLength(0);
  await click(button("Save"));
  expect(writes).toEqual([{ body: { storage_policy: "reference_only" }, manager: "sample-manager" }]);
  expect(host.textContent).toContain("Two items still pending.");
  expect(host.textContent).toContain("A historical correction");
  const facts = Array.from(host.querySelectorAll("section")).find(s => s.querySelector("h2")?.textContent === "Confirmed facts");
  expect(facts?.querySelector("button")).toBeNull();
  await click(button("Edit storage"));
  await click(host.querySelector<HTMLButtonElement>('[role="switch"]'));
  await click(button("Save"));
  expect(writes[1]?.body).toEqual({ storage_policy: "managed" });
});
it("retains a rejected edit and changes only explicit source references; linked chats stay separate", async () => {
  await act(async () => root.render(<Page />));
  openEvidence();
  await click(button("Edit storage"));
  const input = host.querySelector("textarea")!;
  expect(input.value).toBe("https://example.org/project");
  act(() => {
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(input, "https://example.org/notes");
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
  rejectSave = true;
  await click(button("Save"));
  expect(input.value).toBe("https://example.org/notes");
  expect(host.querySelector('[role="alert"]')?.textContent).toContain("Pointer is invalid");
  rejectSave = false;
  await click(button("Save"));
  expect(writes[1]?.body).toEqual({ source_refs: ["https://example.org/notes"] });
  expect(host.querySelector('a[href*="/kb/chats/sample"]')).not.toBeNull();
});
it("keeps a legacy project readable without offering unsupported edits or inferring coverage", async () => {
  knowledge = undefined;
  await act(async () => root.render(<Page />));
  expect(host.textContent).toContain("Two items still pending.");
  expect(host.textContent).not.toContain("Sources and coverage");
  expect(host.textContent).not.toContain("needs review");
  expect(writes).toHaveLength(0);
});

it("keeps historical coverage and writers unknown, including a locked field", async () => {
  knowledge = { policy: "managed", needs_review: false, position: 0, fields: { summary: { status: "unknown", locked: true, maintainer: "user" }, one_line: { status: "unknown", maintainer: "pipeline" } } };
  await act(async () => root.render(<Page />));
  const evidence = openEvidence().textContent!;
  expect(evidence.match(/Coverage unknown/g)).toHaveLength(2);
  expect(evidence.match(/Writer unknown/g)).toHaveLength(2);
  expect(evidence).toContain("Locked");
  expect(evidence).toContain("Maintained by you");
  expect(evidence).toContain("Maintained by Persona");
  expect(evidence).not.toContain("Covers 0");
  expect(evidence).not.toContain("Written by you");
});

it("keeps overlapping field gaps separate, including a gap without a historical cutoff", async () => {
  knowledge = initial();
  knowledge.fields.one_line = { status: "needs_review", uncovered: 1 };
  await act(async () => root.render(<Page list />));
  const row = host.querySelector('a[href*="/kb/projects/sample"]')!;
  expect(row.textContent).toContain("Summary needs review · 3 newer updates");
  expect(row.textContent).toContain("Overview needs review · 1 newer update");
  expect(row.textContent).not.toContain("4 newer updates");
});
