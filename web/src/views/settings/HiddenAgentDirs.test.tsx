// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { HiddenAgentDirs, type HiddenAgentDirsState } from "./HiddenAgentDirs";
import { t } from "../../i18n";

const calls: { method: string; json?: HiddenAgentDirsState; resolve: (v: unknown) => void; reject: (e: Error) => void }[] = [];
vi.mock("../../lib/api", async (orig) => ({ ...await orig<typeof import("../../lib/api")>(), api: (_path: string, options?: { method: string; json: HiddenAgentDirsState }) => new Promise((resolve, reject) => calls.push({ method: options?.method ?? "GET", json: options?.json, resolve, reject })) }));
vi.mock("../../lib/current-assistant", () => ({ useScopedMachines: () => [{ id: "computer", name: "Computer", online: false }, { id: "other", name: "Other", online: true }] }));
let host: HTMLDivElement, root: Root;
beforeEach(() => {
 (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
 calls.length = 0; host = document.createElement("div"); document.body.append(host); root = createRoot(host);
});
afterEach(() => { act(() => root.unmount()); host.remove(); });
const original: HiddenAgentDirsState = { rules: [{ machine_id: "computer", pattern: "~/work/*", enabled: true }, { machine_id: "other", pattern: "/work", enabled: true }] };
async function show() {
 await act(async () => root.render(<HiddenAgentDirs />));
 await act(async () => calls[0].resolve(original));
}
it("shows offline computers and toggles one rule without dropping another computer's rules", async () => {
 await show();
 const sw = host.querySelector<HTMLButtonElement>('[role="switch"]')!;
 expect(sw.getAttribute("aria-checked")).toBe("true");
 await act(async () => sw.click());
 expect(sw.getAttribute("aria-checked")).toBe("false");
 expect(sw.disabled).toBe(true);
 expect(calls[1].json).toEqual({ rules: [{ ...original.rules[0], enabled: false }, original.rules[1]] });
 await act(async () => calls[1].resolve(calls[1].json));
 expect(sw.disabled).toBe(false);
});
it("rolls back rejected changes and displays a retryable failure", async () => {
 await show();
 await act(async () => host.querySelector<HTMLButtonElement>('[role="switch"]')!.click());
 await act(async () => calls[1].reject(new Error("unavailable")));
 expect(host.querySelector('[role="switch"]')!.getAttribute("aria-checked")).toBe("true");
 expect(host.textContent).toContain("unavailable");
 expect(host.textContent).toContain(t("hidden-agent-dirs.retry"));
});
