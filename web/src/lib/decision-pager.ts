import { byTime } from "./times";
import type { Interaction, Item } from "./types";
import type { Todo } from "./todos";

export type DecisionCard = { interaction: Interaction; taskTitle?: string };

export function decisionKey(ix: Interaction): string {
  return JSON.stringify([ix.manager ?? "", ix.machine_id, ix.session_id, ix.id]);
}

export function taskDecisionCards(todos: readonly Todo[], interactions: readonly Interaction[], conversation: string, manager?: string): DecisionCard[] {
  const tasks = new Map<string, string>();
  for (const todo of todos) {
    if (todo.origin !== conversation || todo.done_at || (todo.manager && todo.manager !== manager)) continue;
    for (const agent of todo.agents ?? []) {
      if (agent.released_at) continue;
      const key = JSON.stringify([todo.manager ?? "", agent.machine_id, agent.session_id]);
      if (!tasks.has(key)) tasks.set(key, todo.title);
    }
  }
  return interactions.flatMap((interaction) => {
    const key = JSON.stringify([interaction.manager ?? "", interaction.machine_id, interaction.session_id]);
    const taskTitle = tasks.get(key);
    return taskTitle === undefined ? [] : [{ interaction, taskTitle }];
  });
}

/** Keep the conversation's blocking cards in their incoming order, then show the tasks' oldest cards. */
export function decisionCards(own: readonly Interaction[], tasks: readonly DecisionCard[]): DecisionCard[] {
  const seen = new Set<string>();
  const cards = [...own.map((interaction) => ({ interaction })),
    ...[...tasks].sort((a, b) => byTime(a.interaction.created_at, b.interaction.created_at))];
  return cards.filter(({ interaction }) => {
    const key = decisionKey(interaction);
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}

export function decisionPosition(keys: readonly string[], current: string | undefined, previous: number): number {
  const stillThere = current === undefined ? -1 : keys.indexOf(current);
  return stillThere >= 0 ? stillThere : Math.min(Math.max(previous, 0), keys.length - 1);
}

export function decisionDots(count: number, current: number): number[] {
  const size = Math.min(7, count);
  const first = Math.min(Math.max(current - 3, 0), count - size);
  return Array.from({ length: size }, (_, offset) => first + offset);
}

export function blockedDecisionSkip(ix: Interaction): "terminal" | "terminal_chat" | "permission" | "plan" | undefined {
  if (ix.kind === "permission" || ix.kind === "plan") return ix.kind;
  if (ix.kind === "screen") {
    if (ix.dialog?.command) return "permission";
    return ix.dialog?.options?.some((option) => option.kind === "chat") ? "terminal_chat" : "terminal";
  }
  return undefined;
}

const isPersonaQuestion = (name: string | undefined): boolean => Boolean(name === "ask_user" || name?.endsWith("__ask_user"));
const isQuestionTool = (name: string | undefined): boolean => isPersonaQuestion(name) || name === "AskUserQuestion" || name === "request_user_input";

function firstQuestion(input: unknown): unknown {
  if (typeof input !== "object" || input === null || !("questions" in input) || !Array.isArray(input.questions)) return undefined;
  const first: unknown = input.questions[0];
  return typeof first === "object" && first !== null && "question" in first ? first.question : undefined;
}

/** The message just before the asking tool in the same turn. A task's terminal card uses its latest question call. */
export function decisionSource(ix: Interaction, items: readonly Item[], task: boolean): string | undefined {
  if (!task && ix.kind !== "question") return undefined;
  const question = ix.questions?.[0]?.question;
  let call = items.length - 1;
  for (; call >= Math.max(0, items.length - 400); call--) {
    const item = items[call];
    if (item.kind !== "tool_use") continue;
    if (ix.tool_use_id) {
      if (item.tool_use_id === ix.tool_use_id) break;
    } else if (task) {
      if (isQuestionTool(item.tool_name)) break;
    } else if (question && isPersonaQuestion(item.tool_name) && firstQuestion(item.tool_input) === question) break;
  }
  if (call < Math.max(0, items.length - 400)) return undefined;
  for (let i = call - 1; i >= 0; i--) {
    const item = items[i];
    if (item.kind === "text" && item.role === "user" && !item.tool_use_id) return item.id;
    if (item.kind === "text" && item.role === "assistant" && item.text?.trim()) return item.id;
  }
  return undefined;
}
