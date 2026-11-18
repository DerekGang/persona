import { taskBackgroundWork } from "./background-work";
import { foldedState } from "./child-sessions";
import { shownCloseout } from "./closeout";
import { mostUrgentAgent, type SessionLookup, type Todo } from "./todos";

export const STOP_FOLLOWUP_MS = 60 * 60 * 1000;

/** Execution is independent of unread replies and the supervisor's task lifecycle. */
export function taskExecution(todo: Todo, lookup: SessionLookup) {
  const active = (todo.agents ?? []).filter((a) => !a.released_at && !a.start_error);
  const sessions = active.map((a) => lookup(a.machine_id, a.session_id));
  const running = sessions.some((s) => s && (foldedState(s) === "working" || s.starting || s.work?.phase === "working"))
    || !!taskBackgroundWork(todo, lookup);
  const lead = mostUrgentAgent(todo, lookup)?.session;
  const closeout = running ? undefined : shownCloseout(lead);
  const waiting = !running && closeout?.status !== "done" && sessions.some((s) => s?.work?.phase === "blocked"
    && (s.work.on === "agent" || s.work.on === "external" && !!s.work.wait?.trim()));
  const time = (at: string | undefined | null) => Date.parse(at ?? "") || 0;
  const stoppedAt = Math.max(time(todo.created_at), ...active.map((a, i) =>
    time(sessions[i]?.work?.turn_at) || time(sessions[i]?.state_since) || time(sessions[i]?.updated_at) || time(a.linked_at)));
  const followedAt = Math.max(time(todo.supervision?.followed_up_at),
    ...(todo.supervision?.reviews ?? []).filter((r) => !r.by).map((r) => time(r.at)));
  return { running, waiting, closeout, unattendedSince: Math.max(stoppedAt, followedAt) };
}
