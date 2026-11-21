import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { registerHooks, stripTypeScriptTypes } from 'node:module';
import test from 'node:test';

registerHooks({
  resolve(specifier, context, next) {
    if (specifier === '@kit.ArkTS') return { url: 'worktree:arkts', shortCircuit: true };
    if (specifier.endsWith('platform/HubTransport')) return { url: 'worktree:transport', shortCircuit: true };
    if (specifier.startsWith('.') && context.parentURL?.endsWith('.ets'))
      return { url: new URL(specifier + '.ets', context.parentURL).href, shortCircuit: true };
    return next(specifier, context);
  },
  load(url, context, next) {
    if (url === 'worktree:arkts') return { format: 'module', shortCircuit: true, source:
      "import { randomUUID } from 'node:crypto'; export const util = { generateRandomUUID:randomUUID, Base64Helper:class {decodeSync(b){return Buffer.from(b,'base64')}}, TextDecoder:{create:()=>({decodeToString:b=>new TextDecoder().decode(b)})} };" };
    if (url === 'worktree:transport') return { format: 'module', shortCircuit: true, source:
      'export class HttpFailure extends Error { constructor(status,code,resultUnknown=false,resultPending=false) {super(code);Object.assign(this,{status,code,resultUnknown,resultPending});} }' };
    if (!url.endsWith('.ets')) return next(url, context);
    const source = readFileSync(new URL(url), 'utf8').replace(/@ObservedV2\s*|@Trace\s+/g, '');
    return { format: 'module', source: stripTypeScriptTypes(source), shortCircuit: true };
  }
});
const { WorktreeActions, validWorktreeBranch, checkoutPath } = await import('../entry/src/main/ets/models/WorktreeActions.ets');
const { ProjectWorktree } = await import('../entry/src/main/ets/models/ProjectCatalog.ets');
const { HttpFailure } = await import('worktree:transport');

function fixture() {
  const queries = [], writes = [], blocked = [], finished = [], replayed = [], reads = [], views = new Map();
  const preview = { workspace_id: 'child', kind: 'herdr', path: '/qa/child', branch: 'qa/child', preview_token: 'this-preview', dirty: 0 };
  const removed = { workspace_id: 'child', state: 'removed', removed: true, branch_deleted: true };
  const behavior = { dirty: 0, unknown: false, renameReceipt: { ok: true }, features:['client_ops'], version:'0.9.1',
    workspaces:[{machine_id:'Studio/1',source:'herdr',source_id:'child',cwd:'/qa/child',title:'Child',branch:'main',online:true,available:true}],
    operation:'missing', receiptBody:undefined, replayError:undefined };
  const repo = {
    mutations: { blocked: () => blocked.filter(r=>r.state==='pending'||r.state==='unknown'), finish: async (record, state) => {
      record.state=state;finished.push([record.id,state]);} },
    transport: { async request(path, method, json, id) {
      const body = JSON.parse(json);
      if (path.endsWith('/create')) {
        replayed.push({path,method,json,id});if(behavior.waitReplay) await behavior.waitReplay;if(behavior.replayError)throw behavior.replayError;
        return JSON.stringify(behavior.replayAnswer||{workspace_id:'new-id',branch:body.branch||'automatic',path:body.path||'/qa/automatic'});
      }
      queries.push({ path, method, body });
      if (path.endsWith('/prepare')) return JSON.stringify({ root: '/repo', path: '/qa', branch: 'main', automatic_name: true });
      return JSON.stringify(body.reconcile ? removed : { ...preview, dirty: behavior.dirty });
    }},
    async call(path, method, body, id, admit, _, acknowledged) {
      if (!method || method==='GET') {
        reads.push(path);
        if(path==='/api/v1/health')return behavior.publicHealth?{ok:true,hub_id:'hub-id'}:{ok:true,version:behavior.version,features:behavior.features,hub_id:'hub-id'};
        if(path==='/api/v1/workspaces')return {workspaces:behavior.workspaces,errors:behavior.errors||[]};
        if(path.startsWith('/api/v1/ops/')) {
          if(behavior.operation==='missing')throw new HttpFailure(404,'no such operation');
          return {id:behavior.receiptId||path.split('/').at(-1),kind:behavior.kind||'worktree_create',state:behavior.operation,
            target:{path:behavior.receiptPath||'/api/v1/machines/Studio/1/worktree/create'},
            result:behavior.receiptBody?{status:200,body:Buffer.from(JSON.stringify(behavior.receiptBody)).toString('base64')}:{}};
        }
        throw Error('unexpected read '+path);
      }
      assert.equal(admit(), true); writes.push({ path, method, body, id });
      const result = path.endsWith('/delete') ? removed : path.endsWith('/rename') ? behavior.renameReceipt :
        { workspace_id: 'new-id', path: body.path || '/qa/automatic', branch: body.branch || 'automatic', parent_workspace_id: 'child' };
      if (behavior.unknown || !acknowledged(result)) { blocked.push({ id,path,method,state:'unknown',createdAt:new Date().toISOString() }); throw new HttpFailure(0, 'result_unknown', true); }
      return result;
    }
  };
  const owner = { connected: true, epoch: 3, name: 'home', profile:{hubId:'hub-id'},features:['client_ops'],connectionGeneration() { return this.epoch; },
    verifiedServerVersion(feature){return this.features.includes(feature)?behavior.helloVersion||'':'';},
    captureView(key) {
      const scoped = this.name + ':' + key;
      return {valid: () => true, read: async () => views.get(scoped) || '', write: async value => { views.set(scoped,value); },
        clearIfUnchanged: async expected => { if (views.get(scoped) === expected) views.delete(scoped); }};
    },
    scope() { return this.name; }, owns(epoch, scope) { return this.name === scope && this.epoch === epoch; }, repo: () => repo };
  const root = { ...owner, name: 'root', viewFor: async () => owner };
  const tree = Object.assign(new ProjectWorktree(), { source: 'herdr', sourceId: 'child', machine: 'Studio/1', machineTitle: 'Studio',
    group: 'home', manager: 'default', cwd: '/qa/child', title: 'Child', online: true, startAllowed: true });
  return { form: new WorktreeActions(), root, owner, tree, behavior, writes, queries, finished, blocked, replayed, reads, views };
}

test('typed branch follows the prepared directory, while an explicitly edited directory survives typing', async () => {
  const { form, root, tree, writes } = fixture();
  await form.open(root, tree, 'create');
  assert.equal(form.canSubmit, false);
  form.setBranch('q'); form.setPath('/qa/q'); form.setBranch('qa/Mobile');
  assert.equal(form.path, '/qa/qa-mobile');
  form.setPath('/custom'); form.setBranch('qa/Other'); assert.equal(form.path, '/custom');
  form.setPath(''); assert.equal(form.path, '/qa/qa-other');
  const result = await form.submit();
  assert.equal(result.id, 'new-id'); assert.equal(result.parent, 'child');
  assert.deepEqual(writes[0].body, { workspace_id: 'child', kind: 'herdr', branch: 'qa/Other', path: '/qa/qa-other' });
  assert.equal(writes[0].path, '/api/v1/machines/Studio%2F1/worktree/create');
  assert.equal(writes[0].id.length, 36);
  assert.equal(await form.submit(), undefined);
  assert.equal(writes.length, 1);
  for (const name of ['../bad', 'a.lock', '@', '-x', 'a//b', 'foo@{one', 'a b', 'a~1', 'a:1']) assert.equal(validWorktreeBranch(name), false);
  assert.equal(checkoutPath('C:\\qa', 'fix/One'), 'C:\\qa\\fix-one');
});

test('automatic creation has no stale path, renaming only sends its display name and requires host acknowledgement', async () => {
  const { form, root, tree, writes, behavior } = fixture();
  await form.open(root, tree, 'create'); form.automatic = true; await form.submit();
  assert.deepEqual(writes[0].body, { workspace_id: 'child', kind: 'herdr', auto_name: true });
  await form.open(root, tree, 'rename'); form.name = '  Mobile review  '; await form.submit();
  assert.deepEqual(writes[1].body, { workspace_id: 'child', kind: 'herdr', name: 'Mobile review' });
  behavior.renameReceipt = {};
  await form.open(root, tree, 'rename'); await form.submit();
  assert.equal(form.unknown, true); assert.equal(form.canSubmit, false);
});

test('dirty preview and cancel preserve the checkout, and a clean preview retains its branch by default', async () => {
  const { form, root, tree, behavior, writes, queries } = fixture();
  behavior.dirty = 1; await form.open(root, tree, 'clean');
  assert.equal(form.canSubmit, false); assert.equal(await form.submit(), undefined);
  form.force = true; assert.equal(form.canSubmit, true); form.close();
  assert.equal(writes.length, 0);
  behavior.dirty = 0; await form.open(root, tree, 'clean'); assert.equal(form.force, false);
  assert.deepEqual(queries[1].body, { workspace_id:'child', kind:'herdr', preview:true });
  await form.submit(); assert.equal(writes[0].body.delete_branch, false); assert.equal(writes[0].body.preview_token, 'this-preview');
});

test('a lost cleanup reply is reconciled without issuing a second mutation or leaking its token across owners', async () => {
  const { form, root, tree, behavior, writes, queries, finished } = fixture();
  await form.open(root, tree, 'clean'); form.deleteBranch = true; behavior.unknown = true;
  assert.equal(await form.submit(), undefined); assert.equal(form.unknown, true);
  assert.equal(await form.submit(), undefined);
  const result = await form.reconcile();
  assert.equal(result.action, 'clean'); assert.equal(writes.length, 1); assert.equal(writes[0].body.delete_branch, true);
  assert.equal(queries[1].body.reconcile, true);
  assert.deepEqual(finished, [[writes[0].id,'done']]);
  await form.open(root, tree, 'clean'); root.epoch++; assert.equal(await form.submit(), undefined);
  assert.equal(form.error, 'tree_preview_changed'); assert.equal(writes.length, 1);
});

test('offline machines, missing permission and superseded clean previews report distinct recoveries', async () => {
  const {form,root,owner,tree,queries,writes} = fixture();
  root.connected = false;
  await form.open(root,tree,'clean'); assert.equal(form.error,'offline');
  root.connected = true; tree.online = false;
  await form.open(root,tree,'clean'); assert.equal(form.error,'offline');
  tree.online = true; tree.startAllowed = false;
  await form.open(root,tree,'clean'); assert.equal(form.error,'permission_denied');
  assert.equal(queries.length,0);
  tree.startAllowed = true;
  await form.open(root,tree,'clean'); assert.equal(form.canSubmit,true);
  owner.connected = false;
  await form.submit(); assert.equal(form.error,'offline'); assert.equal(form.canSubmit,false);
  owner.connected = true;
  const entered = Promise.withResolvers(), reply = Promise.withResolvers();
  owner.repo().transport.request = async () => { entered.resolve(); return reply.promise; };
  const preparing = form.open(root,tree,'clean');
  await entered.promise; owner.epoch++; reply.resolve('{}'); await preparing;
  assert.equal(form.error,'tree_preview_changed'); assert.equal(form.canSubmit,false);
  owner.repo().transport.request = async () => { throw new HttpFailure(403,'forbidden'); };
  await form.open(root,tree,'clean'); assert.equal(form.error,'permission_denied');
  owner.repo().transport.request = async () => { owner.connected = false; throw new HttpFailure(0,'transport_failed_closed'); };
  await form.open(root,tree,'clean'); assert.equal(form.error,'offline');
  assert.equal(writes.length,0);
});

test('an invalidated pre-dispatch snapshot removes only its own bytes and a new preview can proceed', async () => {
  for (const replace of [false,true]) {
    const {form,root,owner,tree,writes,views} = fixture();
    const prepared = Promise.withResolvers(), resume = Promise.withResolvers();
    const capture = owner.captureView.bind(owner);
    owner.captureView = key => {
      const lease = capture(key);
      return {...lease, write: async value => {
        await lease.write(value);
        if (value) { prepared.resolve(value); await resume.promise; }
      }};
    };
    await form.open(root,tree,'clean');
    const submitting = form.submit();
    const snapshot = await prepared.promise;
    const [key] = views.keys();
    const replacement = JSON.stringify({...JSON.parse(snapshot),id:'00000000-0000-4000-8000-000000000002'});
    if (replace) { form.close(); views.set(key,replacement); }
    else owner.epoch++;
    resume.resolve(); await submitting;
    assert.equal(writes.length,0);
    assert.equal(views.get(key),replace ? replacement : undefined);
    await form.open(root,tree,'clean');
    assert.equal(form.unknown,replace);
    assert.equal(form.canSubmit,!replace);
  }
});

test('journal admission failure clears a proven unsent operation and preserves an uncertain receipt', async () => {
  for (const uncertain of [false,true]) {
    const {form,root,owner,tree,views,writes} = fixture();
    owner.repo().call = async (_path,_method,_body,_id,admit) => {
      owner.connected = false;
      assert.equal(admit(),false);
      throw new HttpFailure(0,uncertain ? 'receipt_storage_failed' : 'request_updated',uncertain);
    };
    await form.open(root,tree,'clean');
    await form.submit();
    assert.equal(writes.length,0);
    assert.equal(views.size,uncertain ? 1 : 0);
    owner.connected = true;
    await form.open(root,tree,'clean');
    assert.equal(form.unknown,uncertain);
  }
});

test('stale preview cannot bind to a different sheet instance or act on the main checkout', async () => {
  const { form, root, owner, tree, queries } = fixture();
  let complete;
  owner.repo().transport.request = () => new Promise(resolve => { complete = resolve; });
  const opening = form.open(root, tree, 'clean'); while (!complete) await Promise.resolve();
  form.close(); complete(JSON.stringify({ workspace_id:'child', kind:'herdr', path:'/qa', dirty:0, preview_token:'stale' }));
  await opening; assert.equal(form.canSubmit,false);
  tree.main = true; await form.open(root, tree, 'clean'); assert.equal(form.error,'permission_denied');
  assert.equal(queries.length,0);
});

test('a lost create freezes UUID and exact bytes across reopening; one explicit retry does not admit a second operation', async()=>{
  const f=fixture();f.behavior.unknown=true;
  await f.form.open(f.root,f.tree,'create');f.form.setBranch('qa/Once');await f.form.submit();
  const first=f.writes[0], frozen=JSON.stringify(first.body);
  assert.equal(f.form.canSubmit,false);assert.equal(f.form.canRetrySame,true);
  f.form.close();assert.ok([...f.views.values()].some(value=>value.includes(first.id)));
  const other=new WorktreeActions();await other.open(f.root,{...f.tree,cwd:'/other',permits:()=>true},'create');
  assert.equal(other.canRetrySame,false);assert.equal(other.legacy,true);other.close();
  await f.form.open(f.root,f.tree,'create');assert.equal(f.form.branch,'qa/Once');assert.equal(f.form.canSubmit,false);
  f.form.setBranch('qa/changed');f.form.setPath('/new');assert.equal(f.form.path,first.body.path);
  await f.form.reconcile();assert.equal(f.replayed.length,0);
  const gate=Promise.withResolvers();f.behavior.waitReplay=gate.promise;
  const attempt=f.form.reconcile(true);await f.form.reconcile(true);await f.form.submit();
  while (!f.replayed.length) await Promise.resolve();gate.resolve();
  const result=await attempt;assert.equal(result.id,'new-id');
  assert.deepEqual(f.replayed,[{path:first.path,method:first.method,json:frozen,id:first.id}]);
  assert.deepEqual(f.finished,[[first.id,'done']]);assert.equal(f.writes.length,1);assert.equal(f.form.canSubmit,false);
});

test('cached receipts restore completion, pending and definitive failure distinctly',async()=>{
  for(const state of ['done','pending','failed','unknown']){
    const f=fixture();f.behavior.unknown=true;await f.form.open(f.root,f.tree,'create');
    f.form.automatic=true;await f.form.submit();f.behavior.operation=state;
    f.behavior.receiptBody=state==='done'?{workspace_id:'once',branch:'automatic',path:'/qa/automatic'}:{code:'refused'};
    const result=await f.form.reconcile();
    assert.equal(f.replayed.length,0);assert.equal(f.writes.length,1);
    if(state==='done'){assert.equal(result.id,'once');assert.equal(f.blocked[0].state,'done');}
    else if(state==='failed'){assert.equal(f.form.error,'refused');assert.equal(f.form.unknown,false);assert.equal(f.blocked[0].state,'failed');}
    else {assert.equal(f.form.error,state==='pending'?'pending':'result_unknown');assert.equal(f.form.canSubmit,false);}
  }
});

test('an old build, absent feature, invalid target or expired receipt cannot authorize another POST',async()=>{
  for(const kind of ['old','feature','mismatched','expired']){
    const f=fixture();
    if(kind==='old')f.behavior.version='dev';
    if(kind==='feature')f.owner.features=[];
    f.behavior.unknown=true;await f.form.open(f.root,f.tree,'create');f.form.automatic=true;await f.form.submit();
    if(kind==='expired')f.form.requestTime=Date.now()-31*86400000;
    if(kind==='mismatched'){f.behavior.operation='unknown';f.behavior.receiptPath='/api/v1/machines/Another/worktree/create';}
    await f.form.reconcile(true);assert.equal(f.replayed.length,0);assert.equal(f.form.canSubmit,false);
  }
  const f=fixture();f.behavior.version='dev';f.behavior.unknown=true;
  await f.form.open(f.root,f.tree,'create');f.form.automatic=true;await f.form.submit();
  assert.equal(f.form.canRetrySame,false);f.behavior.operation='pending';await f.form.reconcile();
  assert.equal(f.form.canRetrySame,true);
});

test('manual verification of a legacy create releases only its missing record and demands a separate fresh submit',async()=>{
  const f=fixture();
  const old={id:'00000000-0000-4000-8000-000000000001',path:'/api/v1/machines/Studio%2F1/worktree/create',
    method:'POST',state:'unknown',createdAt:new Date(Date.now()-300000).toISOString()};
  const unrelated={...old,id:'00000000-0000-4000-8000-000000000002',path:'/api/v1/machines/Other/worktree/create'};
  f.blocked.push(old,unrelated);
  await f.form.open(f.root,f.tree,'create');assert.equal(f.form.legacy,true);assert.equal(f.form.canRetrySame,false);
  await f.form.reconcile(true);assert.equal(f.form.releaseReady,false);
  f.form.setBranch('qa/lost');await f.form.reconcile(true);assert.equal(f.form.releaseReady,true);assert.equal(f.form.canSubmit,false);
  f.behavior.workspaces.push({...f.behavior.workspaces[0],source_id:'different',branch:'qa/lost'});
  await f.form.reconcile(true);assert.equal(f.form.releaseReady,false);assert.equal(old.state,'unknown');
  f.behavior.workspaces.pop();await f.form.reconcile();await f.form.reconcile(true);
  assert.equal(old.state,'failed');assert.equal(unrelated.state,'unknown');assert.equal(f.form.canSubmit,true);assert.equal(f.replayed.length,0);
  assert.equal(f.writes.length,0);await f.form.submit();assert.equal(f.writes.length,1);assert.notEqual(f.writes[0].id,old.id);
});

test('uncertain rename checks the same live host source, cwd and title without resending the name',async()=>{
  const f=fixture();f.behavior.unknown=true;await f.form.open(f.root,f.tree,'rename');f.form.name='New title';await f.form.submit();
  f.form.close();await f.form.open(f.root,f.tree,'rename');assert.equal(f.form.name,'New title');
  const row=f.behavior.workspaces[0];row.title='New title';
  for(const [key,wrong] of [['machine_id','other'],['source','orca'],['source_id','other'],['cwd','/other'],['online',false],['title','Old']]){
    const original=row[key];row[key]=wrong;await f.form.reconcile(true);row[key]=original;assert.equal(f.form.unknown,true);
  }
  const result=await f.form.reconcile();assert.equal(result.action,'rename');assert.equal(f.writes.length,1);assert.equal(f.replayed.length,0);
});

test('an in-flight retry cannot update or unlock a replacement connection',async()=>{
  const f=fixture();f.behavior.unknown=true;await f.form.open(f.root,f.tree,'create');f.form.automatic=true;await f.form.submit();
  const original=f.writes[0].id, gate=Promise.withResolvers();f.behavior.waitReplay=gate.promise;
  const pending=f.form.reconcile(true);while(!f.replayed.length)await Promise.resolve();
  f.owner.epoch++;f.form.close();gate.resolve();assert.equal(await pending,undefined);
  assert.deepEqual(f.finished,[]);assert.equal(f.blocked[0].id,original);assert.equal(f.form.canSubmit,false);
});

test('a legacy check cannot unlock a young or expired ID, a partial catalogue, a generic 404, or an unverified Hub',async()=>{
  for(const which of ['young','expired','partial','no-source','no-version','wrong-hub','wrong-404']){
    const f=fixture(), age=which==='young'?30000:which==='expired'?32*86400000:300000;
    const record={id:'00000000-0000-4000-8000-000000000003',path:'/api/v1/machines/Studio%2F1/worktree/create',
      method:'POST',state:'unknown',createdAt:new Date(Date.now()-age).toISOString()};
    f.blocked.push(record);
    if(which==='partial')f.behavior.errors=[{machine_id:'Studio/1',error:'unavailable'}];
    if(which==='no-source')f.behavior.workspaces=[];
    if(which==='no-version')f.behavior.version='0.0.1';
    if(which==='wrong-hub')f.owner.profile.hubId='replacement';
    if(which==='wrong-404'){
      const repo=f.owner.repo(),original=repo.call.bind(repo);
      repo.call=(path,...rest)=>path.includes('/ops/')?Promise.reject(new HttpFailure(404,'http_error')):original(path,...rest);
    }
    await f.form.open(f.root,f.tree,'create');f.form.setBranch('qa/lost');await f.form.reconcile();await f.form.reconcile(true);
    assert.equal(f.form.releaseReady,false,which);assert.equal(f.form.canSubmit,false,which);
    assert.equal(record.state,'unknown',which);assert.equal(f.replayed.length,0,which);
  }
});

test('freeze covers manual vs automatic naming and in-place retargets; the body from a different checkout is never used',async()=>{
  const f=fixture();f.behavior.unknown=true;await f.form.open(f.root,f.tree,'create');f.form.setBranch('qa/once');await f.form.submit();
  f.form.automatic=true;assert.equal(f.form.canRetrySame,false);await f.form.reconcile(true);assert.equal(f.replayed.length,0);
  f.form.automatic=false;f.tree.cwd='/other';assert.equal(f.form.canRetrySame,false);await f.form.reconcile(true);
  assert.equal(f.replayed.length,0);assert.equal(f.blocked[0].state,'unknown');
});

test('public health cannot prove support alone; the verified WS build supports safely checking the original create',async()=>{
  for(const hello of ['','0.0.8','0.9.1']){
    const f=fixture();f.behavior.publicHealth=true;f.behavior.helloVersion=hello;f.behavior.unknown=true;
    await f.form.open(f.root,f.tree,'create');f.form.setBranch('qa/hello');await f.form.submit();
    assert.equal(f.form.canRetrySame,hello==='0.9.1');assert.equal(f.replayed.length,0);
    if(hello==='0.9.1')assert.ok(!f.reads.includes('/api/v1/health'),'verified connection metadata needs no public version field');
  }
  const f=fixture();f.behavior.publicHealth=true;f.behavior.helloVersion='0.9.1';f.owner.features=[];f.behavior.unknown=true;
  await f.form.open(f.root,f.tree,'create');f.form.setBranch('qa/hello');await f.form.submit();
  assert.equal(f.form.canRetrySame,false);
});

test('checking a legacy create retries a dropped status and host read once without a replay',async()=>{
  const f=fixture();f.behavior.publicHealth=true;f.behavior.helloVersion='0.9.1';
  f.blocked.push({id:'00000000-0000-4000-8000-000000000003',path:'/api/v1/machines/Studio%2F1/worktree/create',
    method:'POST',state:'unknown',createdAt:new Date(Date.now()-300000).toISOString()});
  const repo=f.owner.repo(),base=repo.call.bind(repo),calls=new Map();
  repo.call=(path,...rest)=>{
    const count=(calls.get(path)||0)+1;calls.set(path,count);
    if(count===1&&(path.includes('/ops/')||path.endsWith('/workspaces')))throw new HttpFailure(0,'transport_failed_42');
    return base(path,...rest);
  };
  await f.form.open(f.root,f.tree,'create');f.form.setBranch('qa/delayed');await f.form.reconcile();
  assert.equal(calls.get('/api/v1/ops/'+f.blocked[0].id),2);assert.equal(calls.get('/api/v1/workspaces'),2);
  assert.equal(f.form.releaseReady,true);assert.equal(f.replayed.length,0);assert.equal(f.writes.length,0);
});

test('a connection change during read backoff cancels the second attempt before it reaches transport',async()=>{
  const f=fixture();f.behavior.unknown=true;await f.form.open(f.root,f.tree,'create');f.form.automatic=true;await f.form.submit();
  const repo=f.owner.repo(),base=repo.call.bind(repo),first=Promise.withResolvers();let count=0;
  repo.call=(path,...rest)=>{
    if(path.includes('/ops/')){count++;first.resolve();throw new HttpFailure(0,'transport_failed_9');}
    return base(path,...rest);
  };
  const check=f.form.reconcile();await first.promise;f.owner.epoch++;await check;
  assert.equal(count,1);assert.equal(f.replayed.length,0);assert.equal(f.form.canCheck,false);
});
