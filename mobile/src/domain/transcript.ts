// A conversation as rows: messages as bubbles, and each run of tool calls as one muted line (the web's chat does the
// same folding with far more detail; this is the demo's minimum).
import type { Item } from "@web/lib/types";

export type Row =
  | { kind: "message"; id: string; role: "user" | "assistant"; text: string; local: boolean; clientId?: string; ts?: string }
  | { kind: "tools"; id: string; names: string[] };

/** `mcp__persona__agents_list` -> `agents_list`. */
export const toolName = (n?: string): string => (n ?? "").replace(/^mcp__[^_]+(?:_[^_]+)*?__/, "") || "tool";

export function transcriptRows(items: readonly Item[]): Row[] {
  const rows: Row[] = [];
  for (const i of items) {
    if (i.kind === "text" && (i.role === "user" || i.role === "assistant")) {
      const text = (i.text ?? "").trim();
      if (!text) continue;
      rows.push({ kind: "message", id: i.id, role: i.role, text, local: i.id.startsWith("local:"), clientId: i.client_msg_id, ts: i.ts });
    } else if (i.kind === "tool_use") {
      const last = rows[rows.length - 1];
      const name = toolName(i.tool_name);
      if (last?.kind === "tools") last.names.push(name);
      else rows.push({ kind: "tools", id: i.id, names: [name] });
    }
  }
  return rows;
}

/** The demo's whole markdown: **bold** runs. The web renders full markdown; a real app would share that renderer. */
export function boldParts(text: string): { text: string; bold: boolean }[] {
  return text
    .split(/\*\*(.+?)\*\*/g)
    .map((part, i) => ({ text: part, bold: i % 2 === 1 }))
    .filter((p) => p.text);
}
