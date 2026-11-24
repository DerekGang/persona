import { byTime } from "./times";
import type { Interaction } from "./types";
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
