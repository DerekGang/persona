import test from 'node:test';
import assert from 'node:assert/strict';
import { registerHooks, stripTypeScriptTypes } from 'node:module';
import { readFileSync } from 'node:fs';

registerHooks({
  resolve(specifier, context, next) {
    if (specifier==='@kit.ArkTS') return {url:'phone:arkts',shortCircuit:true};
    if (specifier === './ClientModel' && context.parentURL?.endsWith('/TodoWorkspace.ets'))
      return { url: 'persona-todo:client', shortCircuit: true };
    if (specifier === './HubRepository' && context.parentURL?.endsWith('/TodoWorkspace.ets'))
      return { url: 'persona-todo:repository', shortCircuit: true };
    if (specifier === '../platform/HubTransport' && context.parentURL?.endsWith('/TodoWorkspace.ets'))
      return { url: 'persona-todo:transport', shortCircuit: true };
    if (context.parentURL?.endsWith('.ets') && specifier.startsWith('.'))
      return { url: new URL(specifier + '.ets', context.parentURL).href, shortCircuit: true };
    return next(specifier, context);
  },
  load(url, context, next) {
    if (url==='phone:arkts') return {format:'module',source:'export const util={generateRandomUUID:()=>crypto.randomUUID()}',shortCircuit:true};
    if (url === 'persona-todo:client') return { format: 'module', source: 'export class ClientModel {}', shortCircuit: true };
    if (url === 'persona-todo:repository') return { format: 'module', source: `
      export function segment(value) { return encodeURIComponent(value); }
      export class HubRepository {}
    `, shortCircuit: true };
    if (url === 'persona-todo:transport') return { format: 'module', source: `
      export class HttpFailure extends Error {
        constructor(status, code, resultUnknown = false, resultPending = false) {
          super(code); this.status = status; this.code = code; this.resultUnknown = resultUnknown; this.resultPending = resultPending;
        }
      }
    `, shortCircuit: true };
    if (url.endsWith('.ets')) {
      const source = readFileSync(new URL(url), 'utf8').replace(/@ObservedV2\s*/g, '').replace(/@Trace\s+/g, '');
      return { format: 'module', source: stripTypeScriptTypes(source), shortCircuit: true };
    }
    return next(url, context);
  }
});

const { assistantTrayRows, TrayRequest }=await import('../entry/src/main/ets/models/AssistantTrays.ets');
const { ResourceRow }=await import('../entry/src/main/ets/models/Wire.ets');
const { composerModelName, validComposerModelID, SessionComposerModes }=await import('../entry/src/main/ets/models/SessionComposerModes.ets');
function session(id,state='idle',details={}) {
  const row=ResourceRow.from({machine_id:'machine',session_id:id,manager:'owner',agent:'claude',launcher:'claude',pane_id:'pane',status:'idle',state,model:'claude-opus-5-5',effort:'high',...details},'session');
  row.group='group';return row;
}
test('main conversation combines manual and handed-out tasks, plans, draft replies, and independent agents; each request has one home',()=>{
  const now=Date.parse('2026-06-01T10:00:00Z'), time=(hours)=>new Date(now+hours*3600e3).toISOString();
  const linked={machine_id:'machine',session_id:'waiting'};
  const todos=[
    {id:'card',title:'Origin card',origin:'main',agents:[linked],created_at:time(-2)},
    {id:'manual',title:'Explicit home',agents:[linked],created_at:time(-1)},
    {id:'held',origin:'other',delivery:{state:'pr',unblock:'user'},created_at:time(-1)},
    {id:'recent',origin:'other',done_at:time(-23)}, {id:'old',origin:'other',done_at:time(-25)},
    {id:'local-done',done_at:time(-1)},{id:'other-manager',manager:'foreign',created_at:time(-1)}
  ];
  const sessions=[session('waiting','needs_you',{todo:{id:'manual'}}),session('standalone','working'),session('assistant','working',{assistant:true}),
    session('far','working',{manager:'foreign'}),session('dropped','working',{todo:{id:'unknown'}})];
  const alien=session('same','needs_you');alien.group='different';sessions.push(alien);
  const feed=[{id:'feed-plan',kind:'plan',ref:'plan'},{id:'reply',kind:'message',ref:'inbox',body:{sender:'Human',draft:'Reply'}},
    {id:'sent',kind:'message',ref:'inbox',body:{sent_at:time(-1)}}], plans={plan:{id:'plan',state:'open',conversation:'main',tasks:[{title:'Plan'}]}};
  const rows=assistantTrayRows(todos,sessions,feed,plans,[new TrayRequest('machine','waiting','ask','Question',now)],'owner','group','main','main',now);
  assert.deepEqual(rows.filter(r=>r.bucket==='needs').map(r=>r.id),['manual','feed-plan','reply','held']);
  assert.deepEqual(rows.filter(r=>r.bucket==='done').map(r=>r.id),['recent']);
  assert.deepEqual(rows.filter(r=>r.bucket==='progress').map(r=>r.kind),['todo','agent']);
  assert.equal(rows.find(r=>r.id==='recent').resource.group,'group');
  assert.equal(rows.find(r=>r.id==='manual').status,'needs_you');assert.equal(rows.find(r=>r.id==='card').status,'working');
  assert.deepEqual(assistantTrayRows(todos,sessions,[],{},[],'owner','group','other','main',now).map(r=>r.id),['held','recent']);
});
function owner() {
  const row=session('chat'), calls=[], trace={scope:'hub-A',generation:1,target:'chat',compose:true,blocked:[],pending:'',afterJournal:undefined};
  const repo={mutations:{blocked:()=>trace.blocked},async call(path,method='GET',body={},op='',admit){
    if(method==='GET') {calls.push({path,method}); return {models:[{id:'default',default:true,resolved:'claude-opus-5-5'},{id:'opus',name:'Opus 5.5',resolved:'claude-opus-5-5'},{id:'sonnet',name:'Sonnet'}]};}
    trace.blocked.push({path,id:op});trace.afterJournal?.();
    if(!admit()) {trace.blocked=trace.blocked.filter(r=>r.id!==op);throw Error('request_updated');}
    calls.push({method,path,body,op});trace.blocked=[];
    return trace.pending ? {pending_effort:trace.pending} : {};
  }};
  const client={selected:row,sessions:[row],connected:true,draft:'untouched draft',decision:undefined,scope:()=>trace.scope,connectionGeneration:()=>trace.generation,
    profile:{manager:'owner',id:'group'},target:()=>trace.target,repo:()=>repo,canCompose:()=>trace.compose,operationBlocked:p=>trace.blocked.some(r=>r.path===p),refresh:async()=>{}};
  return {client,trace,calls,row,model:new SessionComposerModes()};
}
test('focused composer reads live model, offers server models, queues explicit effort and leaves the draft and message endpoint untouched',async()=>{
  const {client,trace,calls,model}=owner();await model.load(client);
  assert.equal(composerModelName('claude-opus-5-5'),'Opus 5.5');assert.equal(composerModelName('other/any'),'other/any');
  assert.equal(model.modelName(client),'Opus 5.5');assert.equal(model.chosenModel(client),'opus');assert.equal(model.currentEffort(client),'high');
  assert.equal(await model.change(client,'model','opus',()=>true),false);
  assert.equal(await model.change(client,'model','unknown',()=>true),false);
  assert.equal(await model.change(client,'model','sonnet',()=>true),true);
  trace.pending='low';assert.equal(await model.change(client,'effort','low',()=>true),true);
  assert.deepEqual(calls.map(c=>[c.method,c.path]),[['GET','/api/v1/machines/machine/launchers/claude/models'],['POST','/api/v1/sessions/machine/chat/modes'],['POST','/api/v1/sessions/machine/chat/modes']]);
  assert.deepEqual(calls[1].body,{model:'sonnet'});assert.deepEqual(calls[2].body,{effort:'low',queue_effort:true});assert.equal(model.pendingEffort(client),'low');
  assert.equal(client.draft,'untouched draft');
});
test('mode writes recheck owner, selected target, capabilities, active form, existing journal and agent activity at storage admission',async()=>{
  for(const change of ['scope','target','capability','decision','pending','view','working']) {
    const {client,trace,calls,row,model}=owner();let visible=true; await model.load(client);
    trace.afterJournal=()=>{
      if(change==='scope') trace.scope='other';
      if(change==='target') trace.target='other';
      if(change==='capability') trace.compose=false;
      if(change==='decision') client.decision={};
      if(change==='pending') trace.blocked.push({path:'/api/v1/sessions/machine/chat/modes',id:'another'});
      if(change==='view') visible=false;
      if(change==='working') row.state='working';
    };
    assert.equal(await model.change(client,'model','sonnet',()=>visible),false,change);
    assert.equal(calls.filter(c=>c.method==='POST').length,0);assert.equal(client.draft,'untouched draft');
  }
});
test('effort-only runtime honors exact advertised levels, suppresses foreign-session collisions and explicit refusal of model changes',async()=>{
  const {client,model,calls}=owner(), codex=session('chat','idle',{agent:'codex',launcher:'codex',efforts:['medium','ultra'],capabilities:{modes:false,model:false}});
  const foreign=session('chat','idle',{manager:'someone-else',model:'private'});client.sessions=[foreign,codex];client.selected=codex;
  await model.load(client);assert.deepEqual(model.efforts(client),['medium','ultra']);assert.deepEqual(model.modeValues(client),[]);assert.equal(model.acceptsModel(client),false);
  assert.equal(calls.length,0);assert.equal(await model.change(client,'effort','max',()=>true),false);assert.equal(await model.change(client,'effort','ultra',()=>true),true);
  client.sessions=[foreign];assert.equal(model.visible(client),false);assert.equal(await model.change(client,'effort','medium',()=>true),false);
});

test('typed launcher models accept the Hub ID grammar, never send the typed string as a message, and honor explicit model support',async()=>{
  for (const id of ['~latest','provider/family:model[1m]','a'.repeat(256)]) assert.ok(validComposerModelID(id),id);
  for (const id of ['~','/foo','bad model','model\nother','非ASCII','a'.repeat(257)]) assert.equal(validComposerModelID(id),false,id);
  const {client,model,calls,row}=owner();await model.load(client);
  assert.equal(await model.change(client,'custom_model','bad model',()=>true),false);
  assert.equal(await model.change(client,'custom_model','vendor/new-id',()=>true),true);
  assert.deepEqual(calls.at(-1).body,{model:'vendor/new-id'});
  row.raw=JSON.stringify({...row.data(),capabilities:{model:false}});
  assert.equal(await model.change(client,'custom_model','vendor/other',()=>true),false);
  assert.equal(calls.filter(c=>c.method==='POST').length,1);assert.equal(client.draft,'untouched draft');
});
