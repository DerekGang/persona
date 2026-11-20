import { expect, test } from "bun:test";
import { connect, Hub, type Contributes, type KBGetResult, type KBListResult, type Manifest } from "./index.ts";
import { testFixtures } from "./test-fixtures.ts";

const fixture = new URL("Manifest.custom-contributions.json", testFixtures);

test("kb.read returns the project's field evidence from the public SDK", async () => {
  const field = { status: "needs_review", cutoff: 0, uncovered: 1, locked: true };
  const project = { policy: "managed", position: 1, needs_review: true, fields: { summary: field } };
  const row = { kind: "projects", id: "example", name: "Example", relevance: 40, updated: "2026-11-04T09:03:00Z" };
  let result: unknown = { items: [{ ...row, project_knowledge: project }], tags: [], total: 1 };
  const hub = new Hub(line => hub.answer({ id: JSON.parse(line).id, result }));
  const list = await hub.call<KBListResult>("kb.list", { kind: "projects" });
  const evidence = list.items[0].project_knowledge;
  expect(evidence?.fields.summary.cutoff).toBe(0);
  expect(evidence?.fields.summary.writer).toBeUndefined();
  expect(evidence?.sources).toBeUndefined();
  const detail = { kind: "projects", id: "example", path: "projects/example", meta: {} };
  const sources = [{ ref: "chats/example-review", state: "merged", state_at: "2026-11-04T09:03:00Z" }];
  result = { detail: { ...detail, project_knowledge: { ...project, sources } } };
  const get = () => hub.call<KBGetResult>("kb.get", { kind: "projects", id: "example" });
  expect((await get()).detail.project_knowledge?.sources?.[0]?.state).toBe("merged");
  result = { detail };
  expect((await get()).detail.project_knowledge).toBeUndefined();
});

test("generated contributions keep typed UI and arbitrary JSON keys", async () => {
  const raw = await Bun.file(fixture).json();
  const manifest: Manifest = raw;
  const fields = manifest.contributes!;
  // This object literal must typecheck: custom keys are allowed without weakening the declared UI field's type.
  const contributes: Contributes = {
    ui: fields.ui,
    "example-core.custompoint": fields["example-core.custompoint"],
    "example-core.rules": [{ id: "second", config: { plugin: null, state: [false, "0", 0] } }],
  };
  expect(contributes.ui?.[0].id).toBe("home");
  expect(JSON.parse(JSON.stringify(manifest))).toEqual(raw);
  expect(Object.hasOwn(fields, "__proto__")).toBe(true);
  expect(Object.hasOwn(fields, "constructor")).toBe(true);
  expect(Object.getPrototypeOf(fields)).toBe(Object.prototype);
  expect(Object.hasOwn(Object.prototype, "polluted")).toBe(false);
});

test("SDK manifest and initialize retain custom contributions", async () => {
  const manifest: Manifest = await Bun.file(fixture).json();
  const answers = new Map<string, (frame: any) => void>();
  const connection = connect({
    id: manifest.id,
    version: manifest.version,
    manifest,
    init: (init) => {
      expect(init.info.id).toBe(manifest.id);
      expect(init.granted).toEqual({});
      return manifest.contributes;
    },
  }, (line) => {
    const frame = JSON.parse(line);
    answers.get(frame.id)?.(frame);
  });
  let seq = 0;
  const call = (method: string, params: unknown = {}) => new Promise<any>((resolve) => {
    const id = `custom-${++seq}`;
    answers.set(id, resolve);
    connection.line(JSON.stringify({ id, method, params }));
  });
  try {
    expect((await call("manifest")).result).toEqual(manifest);
    const response = await call("initialize", {
      protocol: 1,
      hub: { build: 0, extension_points: { ui: 2 } },
      extension: { id: manifest.id, version: manifest.version },
      granted: {},
      active: { ui: ["home"], "example-core.custompoint": ["first"], "example-core.rules": ["second"] },
    });
    expect(response.error).toBeUndefined();
    expect(response.result.contributes).toEqual(manifest.contributes);
    expect(Object.getPrototypeOf(response.result.contributes)).toBe(Object.prototype);
    expect(Object.hasOwn(Object.prototype, "polluted")).toBe(false);
  } finally {
    connection.close();
  }
});
