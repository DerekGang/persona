// Where the hub address and token live. Phones use the secure store (Keychain / Keystore). The web target has no secure
// store, so the demo keeps them in memory and in sessionStorage (gone with the tab); the real app would not do this.
import { Platform } from "react-native";
import * as SecureStore from "expo-secure-store";

const KEY = "persona.hub";
const memory = new Map<string, string>();

export interface Saved {
  base: string;
  token: string;
}

function webStore(): Storage | undefined {
  try {
    return globalThis.sessionStorage;
  } catch {
    return undefined;
  }
}

export async function loadSaved(): Promise<Saved | null> {
  const raw = Platform.OS === "web" ? (memory.get(KEY) ?? webStore()?.getItem(KEY)) : await SecureStore.getItemAsync(KEY);
  if (!raw) return null;
  try {
    const v = JSON.parse(raw) as Partial<Saved>;
    return v.base && v.token ? { base: v.base, token: v.token } : null;
  } catch {
    return null;
  }
}

export async function save(v: Saved | null): Promise<void> {
  const raw = v ? JSON.stringify(v) : "";
  if (Platform.OS === "web") {
    if (v) memory.set(KEY, raw);
    else memory.delete(KEY);
    if (v) webStore()?.setItem(KEY, raw);
    else webStore()?.removeItem(KEY);
    return;
  }
  if (v) await SecureStore.setItemAsync(KEY, raw);
  else await SecureStore.deleteItemAsync(KEY);
}
