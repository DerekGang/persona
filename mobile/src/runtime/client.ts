// The hub's HTTP client: the same headers and retry rules as the web's lib/api.ts, over plain `fetch`.
import { Platform } from "react-native";
import { pairToken } from "@web/lib/auth";

export const enc = encodeURIComponent;
export const READ_TIMEOUT_MS = 45_000;
const RETRY_DELAYS_MS = [300, 1000];

export class ApiError extends Error {
  status: number;
  body: Record<string, unknown>;
  constructor(status: number, message: string, body: Record<string, unknown> = {}) {
    super(message);
    this.status = status;
    this.body = body;
  }
  get code(): string | undefined {
    return typeof this.body.code === "string" ? this.body.code : undefined;
  }
}

export interface Credentials {
  base: string;
  token: string;
}

let creds: Credentials | null = null;
let signedOut: (() => void) | undefined;
let fetcher: typeof fetch = (...a) => fetch(...a);

/** Tests inject a fake fetch. */
export function setFetch(f: typeof fetch) {
  fetcher = f;
}
export function configure(c: Credentials | null) {
  creds = c ? { base: c.base.replace(/\/+$/, ""), token: c.token } : null;
}
export function credentials() {
  return creds;
}
/** Called once a 401 says the saved token is no longer good. */
export function onSignedOut(f: (() => void) | undefined) {
  signedOut = f;
}

export function clientName(): string {
  return Platform.OS === "ios" || Platform.OS === "android" ? Platform.OS : "web";
}

export function uuid(): string {
  const c = (globalThis as { crypto?: { randomUUID?: () => string } }).crypto;
  if (c?.randomUUID) return c.randomUUID();
  return "xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx".replace(/[xy]/g, (ch) => {
    const r = (Math.random() * 16) | 0;
    return (ch === "x" ? r : (r & 3) | 8).toString(16);
  });
}

export interface RequestOptions {
  method?: string;
  json?: unknown;
  /** A token to use instead of the saved one (pairing). */
  token?: string;
  base?: string;
  /** Writes carry an op id so a retry after a lost reply is taken once. */
  opId?: string;
  language?: string;
}

const transient = (status: number) => status === 502 || status === 503 || status === 504;
const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

export async function request<T = unknown>(path: string, opts: RequestOptions = {}): Promise<T> {
  const base = (opts.base ?? creds?.base ?? "").replace(/\/+$/, "");
  const token = opts.token ?? creds?.token ?? "";
  const method = opts.method ?? "GET";
  const write = method !== "GET";
  const headers: Record<string, string> = {
    Authorization: `Bearer ${token}`,
    Accept: "application/json",
    "X-Persona-Client": `${clientName()}/1`,
  };
  if (opts.language) headers["Accept-Language"] = opts.language;
  if (opts.json !== undefined) headers["Content-Type"] = "application/json";
  if (write) headers["X-Persona-Op-Id"] = opts.opId ?? uuid();
  const url = `${base}/api/v1${path}`;
  let last: unknown;
  for (let attempt = 0; attempt <= RETRY_DELAYS_MS.length; attempt++) {
    if (attempt > 0) await sleep(RETRY_DELAYS_MS[attempt - 1]!);
    const ctl = new AbortController();
    const timer = setTimeout(() => ctl.abort(), READ_TIMEOUT_MS);
    try {
      const res = await fetcher(url, { method, headers, body: opts.json === undefined ? undefined : JSON.stringify(opts.json), signal: ctl.signal });
      const text = await res.text();
      let body: Record<string, unknown> = {};
      try {
        const parsed: unknown = text ? JSON.parse(text) : {};
        body = parsed && typeof parsed === "object" ? (parsed as Record<string, unknown>) : {};
      } catch {
        body = {};
      }
      if (res.ok) return body as T;
      if (transient(res.status) && attempt < RETRY_DELAYS_MS.length) {
        last = new ApiError(res.status, `HTTP ${res.status}`, body);
        continue;
      }
      if (res.status === 401 && opts.token === undefined) signedOut?.();
      const message = typeof body.error === "string" ? body.error : typeof body.message === "string" ? body.message : `HTTP ${res.status}`;
      throw new ApiError(res.status, message, body);
    } catch (e) {
      if (e instanceof ApiError) throw e;
      last = e; // a network failure or timeout: retry
    } finally {
      clearTimeout(timer);
    }
  }
  throw last instanceof Error ? last : new Error("request failed");
}

/** Turns what was typed into a saved credential. Mirrors the web's pair(): a bare token, a persona://pair link or a URL
 *  with ?token= is accepted (pairToken is the web's own function, imported in place); the hub's pairing token is traded
 *  for this phone's own device token unless it already is one (`jd_`). */
export async function pair(baseInput: string, input: string, deviceName: string): Promise<Credentials> {
  let base = baseInput.trim().replace(/\/+$/, "");
  if (base && !/^https?:\/\//i.test(base)) base = `https://${base}`;
  const token = pairToken(input);
  await request("/machines", { base, token });
  let use = token;
  if (!token.startsWith("jd_")) {
    try {
      const r = await request<{ token: string; id: string }>("/auth/tokens", { method: "POST", base, token, json: { name: deviceName, kind: "phone" } });
      use = r.token;
    } catch (e) {
      // only an old hub without the endpoint keeps the original credential
      if (!(e instanceof ApiError) || e.status !== 404) throw e;
    }
  }
  return { base, token: use };
}
