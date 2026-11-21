import type { ClientModel } from './ClientModel';
import { ResourceRow, Wire } from './Wire';

const MACHINE_PAGES: Record<string,string> = {
  'phone_agent_folders':'agent-dirs/hidden', 'phone_claude_config':'claude-settings',
  'phone_codex_config':'codex-settings', 'phone_agy_config':'agy-settings'
};

export const FOLLOW_FIELDS: Record<string,string> = {
  'start_requested_tasks':'phone_follow_start_requested', 'main_supervises_own_tasks':'phone_follow_supervise',
  'agent_tasks':'phone_follow_agent_tasks', 'close_ended_terminals':'phone_follow_close_ended', 'agent_auto_naming':'phone_follow_naming'
};

export function agentPreferencePath(page:string, machine:string):string {
  if (page === 'phone_agent_followup') return '/api/v1/assistant';
  const suffix = MACHINE_PAGES[page];
  return suffix && machine ? '/api/v1/machines/' + encodeURIComponent(machine) + '/' + suffix : '';
}

export function agentSettingValue(setting:Record<string,Object>):Object | undefined {
  return setting['value'] ?? setting['default'];
}

export function validAgentSetting(setting:Record<string,Object>,value:Object):boolean {
  const type = Wire.text(setting,'type');
  if (type === 'bool') return typeof value === 'boolean';
  if (type === 'enum') return typeof value === 'string' && Wire.strings(setting,'options').includes(value as string);
  return type === 'string' && typeof value === 'string';
}

export function cleanAgentFolder(input:string):string {
  const path = input.trim();
  return path.length > 1 ? path.replace(/\/+$/, '') || '/' : path;
}

@ObservedV2
export class PhoneAgentSettings {
  @Trace page:string = '';
  @Trace machine:string = '';
  @Trace data:Record<string,Object> = {};
  @Trace loading:boolean = false;
  @Trace saving:boolean = false;
  @Trace error:string = '';
  private model?:ClientModel;
  private scope:string = '';
  private generation:number = -1;
  private serial:number = 0;

  machines(model:ClientModel):ResourceRow[] {
    return model.machines.filter((row:ResourceRow) => (!row.group || row.group === model.profile.id) &&
      (!row.manager || row.manager === model.profile.manager) && row.state !== 'offline' &&
      !Wire.bool(row.data(),'pending') && !Wire.bool(row.data(),'pending_confirm'));
  }

  async open(model:ClientModel,page:string,machine:string = ''):Promise<void> {
    this.close();
    this.model = model; this.page = page; this.scope = model.scope(); this.generation = model.connectionGeneration();
    this.machine = this.machines(model).find((row:ResourceRow) => row.id === machine)?.id || this.machines(model)[0]?.id || '';
    await this.read();
  }

  close():void {
    this.serial++; this.model = undefined; this.scope = ''; this.data = {}; this.error = '';
    this.loading = false; this.saving = false;
  }

  private current(serial:number):boolean {
    return this.serial === serial && !!this.model && this.model.owns(this.generation,this.scope);
  }

  async read():Promise<void> {
    if (this.saving) return;
    const model = this.model, path = agentPreferencePath(this.page,this.machine);
    const serial = ++this.serial;
    if (!model || !this.current(serial) || !path || !model.connected) {
      this.data = {}; this.error = model?.connected ? 'empty' : 'offline'; return;
    }
    this.loading = true; this.error = '';
    try {
      const result = await model.repo().call(path);
      if (this.current(serial)) this.data = result;
    } catch (_) { if (this.current(serial)) this.error='failed'; }
    finally { if (this.current(serial)) this.loading=false; }
  }

  async setOption(key:string,value:Object):Promise<void> {
    if (this.page === 'phone_agent_followup') {
      if (!FOLLOW_FIELDS[key] || typeof this.data[key] !== 'boolean' || typeof value !== 'boolean' || this.data[key] === value) return;
      await this.write(Wire.payload([key],[value])); return;
    }
    const option = Wire.rows(this.data,'settings').find((row:Record<string,Object>) => Wire.text(row,'key') === key);
    if (!option || !validAgentSetting(option,value) || agentSettingValue(option) === value) return;
    await this.write(Wire.payload(['key','value'],[key,value]));
  }

  async changeFolder(pattern:string, replacement:Record<string,Object> | undefined, adding:boolean = false):Promise<void> {
    if (this.page !== 'phone_agent_folders' || !pattern || !Array.isArray(this.data['rules'])) return;
    const before = Wire.rows(this.data,'rules');
    const index = before.findIndex((r:Record<string,Object>) => Wire.text(r,'pattern') === pattern);
    if (adding ? index >= 0 || !replacement : index < 0) return;
    if (replacement && (Wire.text(replacement,'pattern') !== pattern || !['block','hide'].includes(Wire.text(replacement,'action')) ||
      typeof replacement['enabled'] !== 'boolean')) return;
    const next = before.slice();
    if (adding && replacement) next.push(replacement);
    else if (replacement) next.splice(index,1,replacement);
    else next.splice(index,1);
    await this.write(Wire.payload(['rules'],[next]),JSON.stringify(before));
  }

  private async write(body:Record<string,Object>, rulesBefore:string = ''):Promise<void> {
    const model = this.model, path = agentPreferencePath(this.page,this.machine);
    if (!model || !model.connected || !path || this.loading || this.saving || this.error || !this.current(this.serial)) return;
    const serial = ++this.serial;
    this.saving = true;
    try {
      if (rulesBefore) {
        const fresh = await model.repo().call(path);
        if (!this.current(serial)) return;
        if (JSON.stringify(Wire.rows(fresh,'rules')) !== rulesBefore) {
          this.data = fresh; this.error='request_updated'; return;
        }
      }
      const updated = await model.repo().call(path,'PUT',body,'save',() => this.current(serial));
      if (!this.current(serial)) return;
      this.data = updated;
    } catch (_) { if (this.current(serial)) this.error='failed'; }
    finally { if (this.current(serial)) this.saving=false; }
  }
}
