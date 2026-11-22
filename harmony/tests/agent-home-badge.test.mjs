import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {registerHooks,stripTypeScriptTypes} from 'node:module';
import test from 'node:test';

registerHooks({
  resolve(specifier,context,next) {
    if (specifier==='./Wire' && context.parentURL?.endsWith('/AgentAttention.ets')) return {url:new URL('./Wire.ets',context.parentURL).href,shortCircuit:true};
    return next(specifier,context);
  },
  load(url,context,next) {
    if (!url.endsWith('.ets')) return next(url,context);
    const source=readFileSync(new URL(url),'utf8').replace(/@ObservedV2\s*/g,'').replace(/@Trace\s+/g,'');
    return {format:'module',source:stripTypeScriptTypes(source),shortCircuit:true};
  }
});
const {ResourceRow}=await import('../entry/src/main/ets/models/Wire.ets');
const {agentAttentionCards,waitingAgentBadge}=await import('../entry/src/main/ets/models/AgentAttention.ets');
const fixtures=JSON.parse(readFileSync(new URL('../../spec/fixtures/needs-you/cases.json',import.meta.url),'utf8'));
for (const example of fixtures.cases) test('home badge follows the shared Needs You case '+example.name,()=>{
  const {sessions,interactions,mutes,quiet}=example.input;
  const rows=sessions.map(s=>ResourceRow.from(s,'session'));
  const todos=[ResourceRow.from({id:'quiet',quiet:true,agents:quiet.map(key=>{const [machine_id,session_id]=key.split('/');return {machine_id,session_id};})},'todo')];
  const muted=row=>{const key=row.machine+'/'+row.id,space=row.machine+'/'+row.data().space_name;
    return mutes.sessions.includes(key) || !mutes.allowed.includes(key) && mutes.spaces.includes(space);};
  assert.equal(waitingAgentBadge(rows,todos,agentAttentionCards(interactions,'owner'),'group','owner',muted,true),example.expected.badge);
});

test('the home badge isolates Hub and assistant scopes and falls back to session summaries until the Hub answers',()=>{
  const local=ResourceRow.from({session_id:'same',machine_id:'mac',state:'idle',pending_interactions:2},'session');local.group='a';local.manager='one';
  const foreign=ResourceRow.from({session_id:'same',machine_id:'mac',state:'needs_you'},'session');foreign.group='b';foreign.manager='two';
  const cards=agentAttentionCards([{manager:'two',interaction:{id:'elsewhere',session_id:'',machine_id:'mac',kind:'question'}},
    {manager:'one',interaction:{id:'owned',session_id:'',machine_id:'mac',kind:'question'}},
    {manager:'one',interaction:{id:'owned',session_id:'',machine_id:'mac',kind:'question'}}],'one');
  assert.equal(waitingAgentBadge([local,foreign],[],[],'a','one',()=>false,false),1);
  assert.equal(waitingAgentBadge([local,foreign],[],[],'a','one',()=>false,true),0);
  assert.equal(waitingAgentBadge([local,foreign],[],cards,'a','one',()=>false,true),1);
});
