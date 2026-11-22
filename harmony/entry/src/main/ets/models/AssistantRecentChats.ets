import type { ClientModel } from './ClientModel';
import { ResourceRow, Wire } from './Wire';

// Older Hubs identify a scheduled conversation through its task and run history.
// A task that continues the conversation it came from leaves that conversation in Recent.
export class ScheduledChatOrigins {
  private ids: Set<string> = new Set();
  private topics: string[] = [];
  constructor(tasks: Record<string,Object>[], runs: Record<string,Object>[]) {
    const original = new Set(tasks.map((r: Record<string,Object>) => Wire.text(r,'session_id')));
    const add = (id: string): void => { if (id && !original.has(id)) this.ids.add(id); };
    for (const task of tasks) {
      add(Wire.text(task,'run_session_id')); add(Wire.text(task,'last_session_id'));
      const title = Wire.text(task,'title').trim();
      if (title) this.topics.push(title);
    }
    for (const run of runs) add(Wire.text(run,'session_id'));
  }
  contains(row: ResourceRow): boolean {
    if (this.ids.has(row.session || row.id)) return true;
    // The Hub gives older task runs this title and shortens the topic to fit it.
    const match = /^\s*[0-9]{4}\s*\|\s*定时\s*\|\s*(.+?)\s*$/.exec(row.title);
    return !!match && this.topics.some((topic:string) => topic.startsWith(match[1]) || match[1].startsWith(topic));
  }
}

export class SidebarChats {
  recent: ResourceRow[] = [];
  directions: ResourceRow[] = [];
}

export function assistantSidebarChats(conversations: ResourceRow[], main: string, mainMachine: string,
  options: Record<string,Object>, history: ScheduledChatOrigins, group: string, manager: string): SidebarChats {
  const result = new SidebarChats();
  const leaders = Wire.strings(options,'leaders'), background = Wire.text(options,'background_supervisor');
  const showScheduled = Wire.bool(options,'recent_task_chats');
  const leadRows = new Map<string,ResourceRow>(), activity = new Map<ResourceRow,number>();
  for (const row of conversations) {
    const id = row.session || row.id;
    if (!id || row.group && row.group !== group || row.manager && row.manager !== manager ||
        id === main && (!mainMachine || row.machine === mainMachine)) continue;
    const data = row.data();
    if (Wire.bool(data,'archived')) continue;
    if (leaders.includes(id) && id !== main) {
      if (!leadRows.has(id)) leadRows.set(id,row);
      continue;
    }
    const origin = Wire.text(data,'origin').trim();
    if (!showScheduled && (id === background || origin === 'task' ||
        (origin === '' || origin === 'user' || origin === 'channel') && history.contains(row))) continue;
    const value=Wire.text(data,'updated_at'), stamp=Date.parse(value);
    const fraction=value.match(/:\d{2}\.\d{3}(\d+)/);
    activity.set(row,Number.isFinite(stamp) ? stamp + (fraction ? Number('0.'+fraction[1]) : 0) : Number.NEGATIVE_INFINITY);
    result.recent.push(row);
  }
  for (const id of leaders) {
    const row = leadRows.get(id);
    if (row) { result.directions.push(row); leadRows.delete(id); }
  }
  result.recent.sort((a:ResourceRow,b:ResourceRow) => {
    const order = activity.get(b)! - activity.get(a)!;
    const left = a.session || a.id, right = b.session || b.id;
    return order || (left < right ? -1 : left > right ? 1 : 0);
  });
  result.recent = result.recent.slice(0,5);
  return result;
}

@ObservedV2
export class SidebarTaskHistory {
  @Trace revision: number = 0;
  private cache: Map<string,ScheduledChatOrigins> = new Map();
  private empty: ScheduledChatOrigins = new ScheduledChatOrigins([],[]);
  private request: string = '';
  private sequence: number = 0;
  known(scope:string): ScheduledChatOrigins { return this.cache.get(scope) || this.empty; }
  async refresh(owner:ClientModel,visible:boolean): Promise<void> {
    if (!visible || !owner.connected || owner.demo) {
      if (this.request) { this.request=''; this.sequence++; }
      return;
    }
    const scope=owner.scope(), generation=owner.connectionGeneration();
    const key=JSON.stringify([scope,generation,owner.conversations.length]);
    if (key===this.request) return;
    this.request=key;
    const token=++this.sequence, repo=owner.repo();
    try {
      const pages=await Promise.all([repo.call('/api/v1/assistant/tasks'),repo.call('/api/v1/assistant/runs')]);
      if (token!==this.sequence || !owner.owns(generation,scope) || owner.repo()!==repo ||
          !Array.isArray(pages[0]['tasks']) || !Array.isArray(pages[1]['runs'])) return;
      this.cache.set(scope,new ScheduledChatOrigins(Wire.rows(pages[0],'tasks'),Wire.rows(pages[1],'runs')));
      this.revision++;
    } catch (_) { /* Keep the last successful read for this Hub and assistant. */ }
  }
}
