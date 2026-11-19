// How the web shows botmux sessions (docs/protocol/protocol.md "BotMux messages"), switched by the sidebar's botmux chip
// (#2786): "decode" (the default) shows what was written in Lark and the replies the agent sent there; "all" shows the
// transcript as it is; "hide" leaves botmux sessions out of the Agents list (one opened anyway reads decoded). Kept in
// this browser; "persona.agentHideBotmux" is the chip's older show / hide value, read once.
import { useSyncExternalStore } from "react";

export type BotmuxMode = "decode" | "all" | "hide";

export const BOTMUX_MODE_KEY = "persona.agentBotmuxMode";
const LEGACY_HIDDEN_KEY = "persona.agentHideBotmux";

const MODES: readonly BotmuxMode[] = ["decode", "all", "hide"];

/** The stored mode; the default where there is no storage (a server render) or it cannot be read. */
export function readBotmuxMode(store: Storage | undefined = globalThis.localStorage): BotmuxMode {
  try {
    const value = store?.getItem(BOTMUX_MODE_KEY);
    if (MODES.includes(value as BotmuxMode)) return value as BotmuxMode;
    return store?.getItem(LEGACY_HIDDEN_KEY) === "1" ? "hide" : "decode";
  } catch {
    return "decode";
  }
}

/** The chip's next mode: decode → all → hide → decode. */
export const nextBotmuxMode = (m: BotmuxMode): BotmuxMode => MODES[(MODES.indexOf(m) + 1) % MODES.length];

/** Fired on window when the mode changes, for views outside React's store (HistoryBrowser). */
export const BOTMUX_VISIBILITY_EVENT = "persona-botmux-visibility";

const subscribe = (f: () => void) => {
  window.addEventListener(BOTMUX_VISIBILITY_EVENT, f);
  return () => window.removeEventListener(BOTMUX_VISIBILITY_EVENT, f);
};

export const getBotmuxMode = (): BotmuxMode => readBotmuxMode();

export function setBotmuxMode(next: BotmuxMode) {
  if (next === getBotmuxMode()) return;
  localStorage.setItem(BOTMUX_MODE_KEY, next);
  localStorage.removeItem(LEGACY_HIDDEN_KEY);
  window.dispatchEvent(new Event(BOTMUX_VISIBILITY_EVENT));
}

export function useBotmuxMode(): BotmuxMode {
  return useSyncExternalStore(subscribe, getBotmuxMode, getBotmuxMode);
}
