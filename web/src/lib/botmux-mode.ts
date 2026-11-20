// How the web shows botmux sessions (docs/protocol/protocol.md "BotMux messages"), switched by the sidebar's botmux chip
// (#2786): "decode" (the default) shows what was written in Lark and the replies the agent sent there; "all" shows the
// transcript as it is. Session visibility is handled by the shared Agents display setting.
import { useSyncExternalStore } from "react";

export type BotmuxMode = "decode" | "all";

export const BOTMUX_MODE_KEY = "persona.agentBotmuxMode";

/** The stored mode; the default where there is no storage (a server render) or it cannot be read. */
export function readBotmuxMode(store: Storage | undefined = globalThis.localStorage): BotmuxMode {
  try {
    return store?.getItem(BOTMUX_MODE_KEY) === "all" ? "all" : "decode";
  } catch {
    return "decode";
  }
}

export const nextBotmuxMode = (mode: BotmuxMode): BotmuxMode => mode === "decode" ? "all" : "decode";

const BOTMUX_MODE_EVENT = "persona-botmux-mode";

const subscribe = (changed: () => void) => {
  window.addEventListener(BOTMUX_MODE_EVENT, changed);
  window.addEventListener("storage", changed);
  return () => {
    window.removeEventListener(BOTMUX_MODE_EVENT, changed);
    window.removeEventListener("storage", changed);
  };
};

export const getBotmuxMode = (): BotmuxMode => readBotmuxMode();

export function setBotmuxMode(next: BotmuxMode) {
  if (next === getBotmuxMode()) return;
  localStorage.setItem(BOTMUX_MODE_KEY, next);
  window.dispatchEvent(new Event(BOTMUX_MODE_EVENT));
}

export function useBotmuxMode(): BotmuxMode {
  return useSyncExternalStore(subscribe, getBotmuxMode, getBotmuxMode);
}
