import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { registerHooks, stripTypeScriptTypes } from 'node:module';
import test from 'node:test';

registerHooks({
  resolve(specifier, context, next) {
    if (context.parentURL?.endsWith('.ets') && specifier.startsWith('./'))
      return { url:new URL(specifier+'.ets',context.parentURL).href, shortCircuit:true };
    return next(specifier,context);
  },
  load(url, context, next) {
    if (!url.endsWith('.ets')) return next(url,context);
    const source=readFileSync(new URL(url),'utf8').replace(/@ObservedV2\s*/g,'').replace(/@Trace\s+/g,'');
    return {format:'module',source:stripTypeScriptTypes(source),shortCircuit:true};
  }
});
const { AgentDecisionQueue }=await import('../entry/src/main/ets/models/AgentDecisionQueue.ets');
const { interactionResolution }=await import('../entry/src/main/ets/models/InteractionDecision.ets');
const { ResourceRow }=await import('../entry/src/main/ets/models/Wire.ets');

function pair(id) {
  const row=ResourceRow.from({id:'s',session_id:'s',manager:'assistant',machine_id:'machine'},'session');
  row.group=id;
  const state={writes:[],calls:0,blocked:false,canApprove:true,rows:[{id:'ask',session_id:'s',machine_id:'machine',
    kind:'question',questions:[{question:'Which color?',options:[{label:'Blue'},{label:'Green'}]}]}]};
  const repo={async decisions(){ state.calls++; return structuredClone(state.rows); },
    async resolve(request,action,values,op,admit){
      assert.equal(admit(),true); state.writes.push({body:interactionResolution(request,action,values),op}); state.rows=[];
    }};
  const owner={connected:true,profile:{manager:'assistant',id},connectionGeneration(){return 1;},scope(){return id;},
    owns(epoch,scope){return epoch===1&&id===scope;},repo(){return repo;},canApproveSession(){return state.canApprove;},
    operationBlocked(){return state.blocked;}};
  return {row,state,repo,owner};
}
function rootFor(pairs) {
  return {connectionGeneration:()=>1,scope:()=>'root',owns:(n,s)=>n===1 && s==='root',directoryOwners:()=>pairs.map(p=>p.owner),
    directoryOwnerFor:row=>pairs.find(p=>p.row.group===row.group)?.owner};
}

test('identical request IDs on two paired Hubs resolve only on the displayed session owner',async()=>{
  const a=pair('one'), b=pair('two'), root=rootFor([a,b]), queue=new AgentDecisionQueue();
  await queue.reload(root);
  const card=queue.forSession(root,a.row)[0];
  assert.deepEqual(card.quickOptions(),['Blue','Green']);
  await queue.answer(root,b.row,card,'Blue','bad',()=>true);
  assert.equal(b.state.writes.length,0);
  await Promise.all([queue.answer(root,a.row,card,'Blue','one',()=>true),queue.answer(root,a.row,card,'Green','two',()=>true)]);
  assert.deepEqual(a.state.writes,[{body:{answers:{'Which color?':'Blue'}},op:'one'}]);
  assert.equal(card.request.status,'resolved');
  assert.equal(b.state.rows.length,1);
});

test('an outdated request, read-only session, or uncertain mutation cannot be answered from the list',async()=>{
  const p=pair('one'), root=rootFor([p]), queue=new AgentDecisionQueue();
  await queue.reload(root);
  const initial=queue.forSession(root,p.row)[0];
  p.state.canApprove=false;
  assert.equal(queue.enabled(root,p.row,initial),false);
  p.state.canApprove=true; p.state.blocked=true;
  assert.equal(queue.enabled(root,p.row,initial),false);
  p.state.blocked=false; p.state.rows[0].questions[0].question='Different prompt';
  await queue.answer(root,p.row,initial,'Blue','stale',()=>true);
  assert.equal(p.state.writes.length,0);
  assert.equal(queue.error,'request_updated');
  const current=queue.forSession(root,p.row)[0];
  await queue.answer(root,p.row,current,'missing','bad',()=>true);
  assert.equal(p.state.writes.length,0);
  queue.clear();
  assert.equal(queue.enabled(root,p.row,current),false);
});

test('single-owner forms skip unrelated Hubs on open, submit, and refresh',async()=>{
  const a=pair('one'), b=pair('two'), root=rootFor([a,b]), queue=new AgentDecisionQueue();
  await queue.reload(root,a.owner);
  const card=queue.forSession(root,a.row)[0];
  await queue.answerForm(root,a.row,card,'answer',{'Which color?':'Green'},'only',()=>true);
  assert.equal(a.state.writes[0].body.answers['Which color?'],'Green');
  assert.equal(b.state.calls,0);
  assert.equal(queue.forSession(root,b.row).length,0);
});

test('navigation or owner changes while validating a response prevent the mutation',async()=>{
  const p=pair('one'),root=rootFor([p]),queue=new AgentDecisionQueue();
  await queue.reload(root);
  const card=queue.forSession(root,p.row)[0], finish=Promise.withResolvers();
  p.repo.decisions=()=>finish.promise;
  const answer=queue.answer(root,p.row,card,'Blue','stale',()=>true);
  queue.clear(); finish.resolve(p.state.rows);
  await answer;
  assert.equal(p.state.writes.length,0);
});
