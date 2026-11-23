// Stands in for web/src/lib/api.ts. A few shared modules (todos, providers, model config) import hub calls from it;
// the demo's shared surface does not call them, so `api` is wired to the mobile client and the rest is inert.
import { ApiError, enc, request } from "../runtime/client";
import { t } from "./i18n";

export { ApiError, enc };
export const api = <T = unknown>(path: string, opts: { method?: string; json?: unknown; token?: string } = {}) => request<T>(path, opts);
export const pastHub = <T>(p: Promise<T>) => p;
export const credentialOwner = () => "";
export const onCredentialChange = (_f: () => void) => () => {};
export const subscribeCredentialOwner = (_f: () => void) => () => {};
export const setPairedToken = (_token: string, _id?: string) => {};
export function errorText(e: unknown): string {
  if (e instanceof ApiError) return e.status === 502 || e.status === 503 || e.status === 504 ? t("lib-api.hub_unavailable", { detail: `HTTP ${e.status}` }) : e.message;
  if (e instanceof TypeError) return t("lib-api.cant_reach_hub");
  return e instanceof Error ? e.message : String(e);
}
