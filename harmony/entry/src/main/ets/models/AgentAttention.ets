import { ResourceRow, Wire } from './Wire';

/** Only routing and visibility take part in the badge, so a summary need never retain the user's answer or prompt. */
export class AgentAttentionCard {
  readonly id: string;
  readonly machine: string;
  readonly session: string;
  readonly parent: string;
  readonly held: boolean;
  readonly hidden: boolean;

  constructor(id: string, machine: string, session: string, parent: string, held: boolean, hidden: boolean) {
    this.id = id;
    this.machine = machine;
    this.session = session;
    this.parent = parent;
    this.held = held;
    this.hidden = hidden;
  }
}

export function agentAttentionCards(input: Record<string, Object>[], manager: string): AgentAttentionCard[] {
  const result: AgentAttentionCard[] = [];
  const seen = new Set<string>();
  for (const envelope of input) {
    const nested = Wire.object(envelope, 'interaction');
    const card = Object.keys(nested).length ? nested : envelope;
    const owner = Wire.text(envelope, 'manager') || Wire.text(envelope, 'manager_id') ||
      Wire.text(card, 'manager') || Wire.text(card, 'manager_id') || manager;
    const id = Wire.text(card, 'id');
    const machine = Wire.text(card, 'machine_id') || Wire.text(envelope, 'machine_id');
    const session = Wire.text(card, 'session_id') || Wire.text(envelope, 'session_id');
    const key = JSON.stringify([machine, session, id]);
    const status = Wire.text(envelope, 'status') || Wire.text(card, 'status');
    if (owner !== manager || !id || !machine || seen.has(key) ||
      ['resolved', 'closed', 'expired', 'dismissed', 'cancelled'].includes(status)) continue;

    seen.add(key);
    const parent = Wire.text(card, 'folded_under') || Wire.text(envelope, 'folded_under');
    const held = (Wire.bool(card, 'delegating') || Wire.bool(envelope, 'delegating')) && Wire.text(card, 'kind') !== 'screen';
    const hidden = Wire.bool(card, 'hidden_by_directory') || Wire.bool(envelope, 'hidden_by_directory') ||
      Wire.bool(card, 'muted') || Wire.bool(envelope, 'muted');
    result.push(new AgentAttentionCard(id, machine, session, parent, held, hidden));
  }
  return result;
}

function sessionKey(machine: string, session: string): string {
  return JSON.stringify([machine, session]);
}

/** Match the shared Needs You corpus for a single connected Hub and assistant, independent of active list filters. */
export function waitingAgentBadge(rows: ResourceRow[], todos: ResourceRow[], cards: AgentAttentionCard[],
  group: string, manager: string, muted: (row: ResourceRow) => boolean, authoritative: boolean): number {
  const quietTodos = new Set<string>();
  const quietAgents = new Set<string>();
  for (const todo of todos) {
    if (todo.group && todo.group !== group || todo.manager && todo.manager !== manager) continue;
    const data = todo.data();
    if (!Wire.bool(data, 'quiet')) continue;
    quietTodos.add(todo.id);
    for (const agent of Wire.rows(data, 'agents')) {
      quietAgents.add(sessionKey(Wire.text(agent, 'machine_id'), Wire.text(agent, 'session_id')));
    }
  }

  const sessions = new Map<string, ResourceRow>();
  const waiting = new Set<string>();
  const excluded = new Set<string>();
  for (const row of rows) {
    if (row.kind !== 'session' || row.group && row.group !== group || row.manager && row.manager !== manager) continue;
    const key = sessionKey(row.machine, row.session || row.id);
    const data = row.data();
    sessions.set(key, row);
    if (Wire.bool(data, 'assistant') || Wire.bool(data, 'hidden_by_directory') || Wire.bool(data, 'muted') ||
      Wire.bool(data, 'quiet') || quietTodos.has(Wire.text(Wire.object(data, 'todo'), 'id')) || quietAgents.has(key) || muted(row)) {
      excluded.add(key);
    }
    const needsUser = ['needs_you', 'blocked', 'waiting_for_user'].includes(row.state) ||
      Wire.number(Wire.object(data, 'children'), 'needs_you') > 0;
    if (needsUser || !authoritative && Wire.number(data, 'pending_interactions') > 0) waiting.add(key);
  }
  for (const row of sessions.values()) {
    const session = row.session || row.id;
    const parent = Wire.text(row.data(), 'folded_under');
    if (parent && parent !== session && sessions.has(sessionKey(row.machine, parent))) waiting.delete(sessionKey(row.machine, session));
  }

  const active = new Set<string>();
  const held = new Set<string>();
  for (const card of cards) {
    const own = sessionKey(card.machine, card.session);
    if (card.hidden || excluded.has(own) || quietAgents.has(own)) continue;
    const parent = sessionKey(card.machine, card.parent);
    let key = own;
    if (!card.session) key = JSON.stringify([card.machine, '', card.id]);
    else if (card.parent && waiting.has(parent)) key = parent;
    if (excluded.has(key)) continue;
    if (card.held) held.add(key);
    else active.add(key);
  }
  for (const key of waiting) {
    if (!excluded.has(key) && !held.has(key)) active.add(key);
  }
  return active.size;
}
