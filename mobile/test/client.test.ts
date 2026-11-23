import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError, configure, onSignedOut, pair, request, setFetch } from "../src/runtime/client";

type Call = { url: string; init: RequestInit };
const reply = (status: number, body: unknown) => new Response(JSON.stringify(body), { status });

let calls: Call[];
function fake(...responses: (Response | Error)[]) {
  calls = [];
  const queue = [...responses];
  setFetch((async (url: string, init: RequestInit) => {
    calls.push({ url, init });
    const r = queue.shift() ?? reply(200, {});
    if (r instanceof Error) throw r;
    return r;
  }) as unknown as typeof fetch);
}

beforeEach(() => {
  vi.useFakeTimers();
  configure({ base: "http://hub.test/", token: "tok" });
});
afterEach(() => {
  vi.useRealTimers();
  onSignedOut(undefined);
});

const run = async <T>(p: Promise<T>) => {
  const settled = p.then((v) => ({ v }), (e: unknown) => ({ e }));
  await vi.runAllTimersAsync();
  return settled;
};

describe("request", () => {
  it("sends the bearer token, client name and language, and no op id on a read", async () => {
    fake(reply(200, { ok: 1 }));
    const r = await run(request("/machines", { language: "ja" }));
    expect(r).toEqual({ v: { ok: 1 } });
    expect(calls[0]!.url).toBe("http://hub.test/api/v1/machines");
    const h = calls[0]!.init.headers as Record<string, string>;
    expect(h.Authorization).toBe("Bearer tok");
    expect(h["X-Persona-Client"]).toMatch(/^web\/1$/);
    expect(h["Accept-Language"]).toBe("ja");
    expect(h["X-Persona-Op-Id"]).toBeUndefined();
  });

  it("gives a write an op id, kept across its retries", async () => {
    fake(reply(503, {}), reply(200, { ok: true }));
    const r = await run(request("/x", { method: "POST", json: { a: 1 }, opId: "op-1" }));
    expect(r).toEqual({ v: { ok: true } });
    expect(calls).toHaveLength(2);
    expect((calls[1]!.init.headers as Record<string, string>)["X-Persona-Op-Id"]).toBe("op-1");
    expect(calls[1]!.init.body).toBe('{"a":1}');
  });

  it("retries a network failure twice, then throws it", async () => {
    fake(new TypeError("down"), new TypeError("down"), new TypeError("down"));
    const r = await run(request("/machines"));
    expect(calls).toHaveLength(3);
    expect((r as { e: Error }).e).toBeInstanceOf(TypeError);
  });

  it("does not retry a refusal, and carries the hub's code", async () => {
    fake(reply(409, { error: "taken", code: "op_id_reused" }));
    const r = (await run(request("/x", { method: "POST", json: {} }))) as { e: ApiError };
    expect(calls).toHaveLength(1);
    expect(r.e).toBeInstanceOf(ApiError);
    expect(r.e.status).toBe(409);
    expect(r.e.code).toBe("op_id_reused");
    expect(r.e.message).toBe("taken");
  });

  it("reports a 401 as signed out", async () => {
    const out = vi.fn();
    onSignedOut(out);
    fake(reply(401, { error: "no" }));
    await run(request("/machines"));
    expect(out).toHaveBeenCalledOnce();
  });
});

describe("pair", () => {
  it("adds https:// to an address typed without a scheme", async () => {
    fake(reply(200, { machines: [] }));
    const r = await run(pair("hub.test", "jd_mine", "p"));
    expect(r).toEqual({ v: { base: "https://hub.test", token: "jd_mine" } });
  });

  it("takes the token out of a pairing link, probes the hub and trades it for a device token", async () => {
    fake(reply(200, { machines: [] }), reply(200, { token: "jd_device", id: "t1" }));
    const r = await run(pair("http://hub.test/", "persona://pair?token=jp_abc", "My phone"));
    expect(r).toEqual({ v: { base: "http://hub.test", token: "jd_device" } });
    expect((calls[0]!.init.headers as Record<string, string>).Authorization).toBe("Bearer jp_abc");
    expect(calls[1]!.url).toBe("http://hub.test/api/v1/auth/tokens");
    expect(JSON.parse(String(calls[1]!.init.body))).toEqual({ name: "My phone", kind: "phone" });
  });

  it("keeps a device token as it is, without trading", async () => {
    fake(reply(200, { machines: [] }));
    const r = await run(pair("http://hub.test", "jd_mine", "My phone"));
    expect(r).toEqual({ v: { base: "http://hub.test", token: "jd_mine" } });
    expect(calls).toHaveLength(1);
  });

  it("keeps the original token on an old hub without the endpoint (404), and fails on a wrong token", async () => {
    fake(reply(200, {}), reply(404, {}));
    expect(await run(pair("http://hub.test", "jp_old", "p"))).toEqual({ v: { base: "http://hub.test", token: "jp_old" } });
    fake(reply(401, { error: "bad token" }));
    const r = (await run(pair("http://hub.test", "jp_bad", "p"))) as { e: ApiError };
    expect(r.e.status).toBe(401);
  });
});
