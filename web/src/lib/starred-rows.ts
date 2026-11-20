// Chat and sidebar only need the compact index. Load the requests and write
// handling separately from the first screen and never pull snapshot text here.
import { useEffect, useSyncExternalStore } from "react";

/** Transcript location as the chat already holds it. */
export type StarOrigin = { mid: string; sid: string; item: string };

export interface StarSource {
  machine_id: string;
  session_id: string;
  item_id: string;
  conversation: "assistant" | "agent";
}

export interface StarInput extends StarSource {
  role: "user" | "assistant";
  title: string;
  speaker: string;
  source_manager?: string;
  written_at?: string;
  text: string;
  omitted_chars?: number;
}

export interface Star extends StarInput {
  id: string;
  starred_at: string;
}

export const starKey = (s: StarSource) => JSON.stringify([s.machine_id, s.session_id, s.item_id]);

type StarMark = { id?: string; pending?: "add" | "remove" };
const empty: StarMark = {};
let rows: ReadonlyMap<string, StarMark> = new Map();
let count: number | undefined;
let writable = false;
let chunk: Promise<typeof import("./starred")> | undefined;
const listeners = new Set<() => void>();
const subscribe = (listener: () => void) => (listeners.add(listener), () => void listeners.delete(listener));

export function starredModule() {
  chunk ??= import("./starred").catch((error: unknown) => {
    chunk = undefined;
    throw error;
  });
  return chunk;
}

export function setStarredRows(next: ReadonlyMap<string, StarMark>, total: number | undefined, canWrite: boolean) {
  rows = new Map([...next].map(([key, mark]) => {
    const previous = rows.get(key);
    return [key, previous && previous.id === mark.id && previous.pending === mark.pending ? previous : mark];
  }));
  count = total;
  writable = canWrite;
  listeners.forEach((l) => l());
}

function start() {
  void starredModule().then((m) => m.startStarred(), () => {});
}

export function useStarredCount(enabled: boolean) {
  useEffect(() => { if (enabled) start(); }, [enabled]);
  return useSyncExternalStore(subscribe, () => enabled ? count : undefined, () => undefined);
}

export function useStarMark(source: StarSource | undefined): StarMark {
  const key = source && starKey(source);
  return useSyncExternalStore(subscribe, () => (key && rows.get(key)) || empty, () => empty);
}

export function useCanStar() {
  return useSyncExternalStore(subscribe, () => writable, () => false);
}
