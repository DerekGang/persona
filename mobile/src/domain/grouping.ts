// The Agents list: what needs you first, then the rest by project. Pure selectors over the web's own helpers
// (stateOf, byAttention, repoOf are imported in place).
import type { Machine, Session } from "@web/lib/types";
import { byAttention, stateOf } from "@web/lib/format";
import { repoOf } from "@web/lib/agent-repos";

export interface Section {
  key: string;
  /** Project name (the repository folder); empty for the Needs you section. */
  title: string;
  needsYou: boolean;
  rows: Session[];
}

/** Agents a person works with: not the assistant's own conversations, not hidden ones. */
export const agentRows = (all: readonly Session[]): Session[] => all.filter((s) => !s.assistant && !s.hidden_by_directory);

export function projectOf(s: Session) {
  return repoOf(s.cwd ?? "", s.repo_root);
}

export function groupAgents(all: readonly Session[]): Section[] {
  const rows = agentRows(all).sort(byAttention);
  const needs = rows.filter((s) => stateOf(s) === "needs_you");
  const rest = rows.filter((s) => stateOf(s) !== "needs_you");
  const sections: Section[] = [];
  if (needs.length) sections.push({ key: "needs-you", title: "", needsYou: true, rows: needs });
  const byProject = new Map<string, Section>();
  for (const s of rest) {
    const p = projectOf(s);
    const key = p.repo || "-";
    let sec = byProject.get(key);
    if (!sec) {
      sec = { key, title: p.name, needsYou: false, rows: [] };
      byProject.set(key, sec);
      sections.push(sec);
    }
    sec.rows.push(s);
  }
  return sections;
}

/** A computer's name, or "" until the list has loaded: a raw id never shows on a primary row. */
export const machineName = (machines: readonly Machine[], id: string): string => machines.find((m) => m.id === id)?.name ?? "";
