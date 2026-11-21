import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {registerHooks,stripTypeScriptTypes} from 'node:module';
import test from 'node:test';

registerHooks({
  resolve(specifier,context,next) {
    if (context.parentURL?.endsWith('/PhoneAgentSettings.ets') && specifier==='./Wire')
      return {url:new URL('./Wire.ets',context.parentURL).href,shortCircuit:true};
    return next(specifier,context);
  },
  load(url,context,next) {
    if (!url.endsWith('.ets')) return next(url,context);
    const source=readFileSync(new URL(url),'utf8').replace(/@ObservedV2\s*/g,'').replace(/@Trace\s+/g,'');
    return {format:'module',source:stripTypeScriptTypes(source),shortCircuit:true};
  }
});
const {PhoneAgentSettings,agentPreferencePath,validAgentSetting,agentSettingValue,cleanAgentFolder}=
  await import('../entry/src/main/ets/models/PhoneAgentSettings.ets');
const {ResourceRow}=await import('../entry/src/main/ets/models/Wire.ets');

function harness(request) {
  const calls=[],state={generation:1,scope:'work'};
  const machine=(id,manager,connected=true) => ResourceRow.from({id,machine_id:id,manager,state:connected?'online':'offline'},'machine');
  const rows=[machine('my/mac','work'),machine('else','other'),machine('gone','work',false)];
  const repository={async call(path,verb='GET',body={},op='',admit){
    calls.push({path,verb,body,op});
    return request(path,verb,body,admit);
  }};
  const model={profile:{id:'h',manager:'work'},machines:rows,connected:true,repo:()=>repository,
    connectionGeneration:()=>state.generation,scope:()=>state.scope,owns:(gen,scope)=>gen===state.generation && scope===state.scope};
  return {model,state,calls};
}
test('machine choices and preference writes use the selected owner and typed server controls',async()=>{
  const enumOption={key:'approval',type:'enum',options:['ask','allow'],default:'ask'};
  const fields=[enumOption,{key:'visible',type:'bool',default:true,value:false}];
  const env=harness(async (_path,verb,body,admit)=>{
    if (verb==='PUT') {assert.ok(admit());fields[0].value=body.value}
    return {settings:fields};
  });
  const settings=new PhoneAgentSettings();await settings.open(env.model,'phone_codex_config','else');
  assert.deepEqual(settings.machines(env.model).map(m=>m.id),['my/mac']);
  assert.equal(agentPreferencePath('phone_codex_config',settings.machine),'/api/v1/machines/my%2Fmac/codex-settings');
  assert.equal(agentSettingValue(fields[1]),false);assert.equal(validAgentSetting(enumOption,false),false);
  await settings.setOption('unknown',true);await settings.setOption('approval','outside');await settings.setOption('approval','ask');
  assert.equal(env.calls.length,1);
  await settings.setOption('approval','allow');assert.equal(env.calls.length,2);
  assert.deepEqual(env.calls[1].body,{key:'approval',value:'allow'});
  assert.equal(env.calls[1].op,'','the durable mutation store must issue a fresh id for each user operation');
});
test('a newer folder rule read supersedes the local draft without replacing another client’s rules',async()=>{
  const rule={pattern:'~/weather',action:'hide',enabled:true};let reads=0;
  const env=harness(async()=>({rules:++reads===1?[rule]:[rule,{pattern:'*/archive',action:'block',enabled:true}]}));
  const settings=new PhoneAgentSettings();await settings.open(env.model,'phone_agent_folders');
  await settings.changeFolder(rule.pattern,{...rule,enabled:false});
  assert.equal(settings.error,'request_updated');assert.equal(settings.data.rules.length,2);
  assert.ok(env.calls.every(c=>c.verb==='GET'));
  assert.equal(cleanAgentFolder(' ~/weather/// '),'~/weather');
  for(const bad of ['relative','/','*','~/']) assert.equal(cleanAgentFolder(bad),'');
});
test('a late machine read and an admitted save cannot cross an owner change',async()=>{
  let release, entered;
  const ready=new Promise(resolve=>{entered=resolve});
  const gate=new Promise(resolve=>{release=resolve});
  const env=harness(async(_path,verb,_body,admit)=>{
    if (verb==='PUT') {entered();await gate;assert.equal(admit(),false);throw new Error('request_updated')}
    return {agent_tasks:true};
  });
  const settings=new PhoneAgentSettings();await settings.open(env.model,'phone_agent_followup');
  await settings.setOption('made_up',true);const pending=settings.setOption('agent_tasks',false);
  await ready;env.state.generation++;settings.close();release();await pending;
  assert.deepEqual(settings.data,{});assert.equal(settings.error,'');
});
