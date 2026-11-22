import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {registerHooks,stripTypeScriptTypes} from 'node:module';

registerHooks({
  resolve(spec,ctx,next) {
    if(ctx.parentURL?.endsWith('.ets') && spec.startsWith('.')) return {url:new URL(spec+'.ets',ctx.parentURL).href,shortCircuit:true};
    return next(spec,ctx);
  },
  load(url,ctx,next) {
    if(!url.endsWith('.ets')) return next(url,ctx);
    const code=readFileSync(new URL(url),'utf8').replace(/@ObservedV2\s*/g,'').replace(/@Trace\s+/g,'');
    return {format:'module',shortCircuit:true,source:stripTypeScriptTypes(code)};
  }
});
const {MeetingHubChoice}=await import('../entry/src/main/ets/models/MeetingHubAudio.ets');
const source=readFileSync(new URL('../entry/src/main/ets/components/KnowledgeWorkspacePage.ets',import.meta.url),'utf8');
const recording=readFileSync(new URL('../entry/src/main/ets/components/KnowledgeRecordingView.ets',import.meta.url),'utf8');
const strings=new Map(JSON.parse(readFileSync(new URL('../entry/src/main/resources/en_US/element/string.json',import.meta.url))).string.map(s=>[s.name,s.value]));
const tr=key=>strings.get(key) || key;
const methods=source.slice(source.indexOf('  private resetMeetingMode()'),source.indexOf('  @Local meetingTitle:'))+
  source.slice(source.indexOf('  private requestRecordingStart()'),source.indexOf('  // Calendar titles'));
const NativeStart=new Function('MeetingHubChoice','meetingAudioNetwork','tr','nativeMeetingServices',
  'return '+stripTypeScriptTypes('class Start {'+methods+'}'));
const settle=()=>new Promise(resolve=>setImmediate(resolve));

function fixture(){
  const network={wifiOnly:true},started=[],dialogs=[],state={generation:1,scope:'hub/personal',service:'Isolated transcription'};
  const page=new (NativeStart(MeetingHubChoice,network,tr,{setRecordingEvent(){}}))();
  const owner={profile:{id:'test',manager:'personal',hubId:'hub',baseUrl:'https://fixture.invalid',pin:'synthetic'},
    connected:true,demo:false,features:['meeting_conversation','meeting_live_view','meeting_transcription'],
    scope:()=>state.scope,connectionGeneration:()=>state.generation,owns:(g,s)=>g===state.generation && s===state.scope};
  Object.assign(page,{model:owner,recordingScope:()=>state.scope,meetingKind:'meeting',meetingTitle:'Synthetic weekly',
    privateMeeting:false,selectedCalendarEvent:'',roomRole:()=>'off',pageAvailable:true,visible:true,confirmVersion:0,selectedAudioOwner:'',
    meetingNetworkReady:true,meetingSpeech:{offered:true,ready:true,mode:'hub',extensionRef:'isolated/asr',serviceName:()=>state.service,retentionDays:14},
    getUIContext:()=>({showAlertDialog:x=>dialogs.push(x)}),loadRecordings:async()=>true,
    recording:{canStart:()=>true,start:async(...args)=>{started.push(args);return true;}}});
  return {page,state,network,started,dialogs,owner};
}

test('both capture buttons use the same single-meeting admission and selecting a cloud service opens no recorder',async()=>{
  assert.match(source,/onStart:\(\):void => this\.requestRecordingStart\(\)/);
  assert.match(source,/else this\.requestRecordingStart\(\);\s*\}\)\.id\('recordings\.capture'\)/);
  assert.match(recording,/onClick\(\(\) => this\.onStart\(\)\)\.id\('knowledge\.recording\.start'\)/);
  const {page,started,dialogs}=fixture();
  assert.equal(page.pickedHub(),false,'a server default never consents to sending audio');
  page.chooseMode(true);assert.equal(page.pickedHub(),true);assert.deepEqual(started,[]);assert.deepEqual(dialogs,[]);
  page.requestRecordingStart();assert.equal(dialogs.length,1);assert.match(dialogs[0].message,/Isolated transcription/);assert.match(dialogs[0].message,/14 days/);
  dialogs[0].primaryButton.action();dialogs[0].secondaryButton.action();await settle();assert.deepEqual(started,[]);
  page.requestRecordingStart();dialogs[1].secondaryButton.action();dialogs[1].secondaryButton.action();await settle();
  assert.equal(started.length,1);assert.equal(started[0][0],'meeting');assert.equal(started[0][2],'off');
  const choice=started[0][4];assert.ok(choice instanceof MeetingHubChoice);assert.ok(choice.matches(page.model));
  assert.equal(choice.serviceRef,'isolated/asr');assert.equal(choice.serviceName,'Isolated transcription');assert.equal(choice.wifiOnly,true);
  assert.equal(page.pickedHub(),false,'the same choice may not carry into another meeting');
  page.requestRecordingStart();await settle();assert.equal(started.at(-1)[4],undefined);assert.equal(dialogs.length,2);
});

test('an old, unready, offline, unsupported or uninitialized route and a note capture remain on this device',async()=>{
  for(const change of [f=>f.page.meetingSpeech.ready=false,f=>f.page.meetingSpeech.offered=false,f=>f.owner.connected=false,
    f=>f.page.meetingNetworkReady=false,f=>f.page.setMeetingKind('note'),f=>f.owner.features.pop(),f=>f.owner.features.shift(),f=>f.owner.features.splice(1,1)]) {
    const f=fixture();f.page.chooseMode(true);change(f);f.page.chooseMode(true);assert.equal(f.page.pickedHub(),false);
    f.page.requestRecordingStart();await settle();assert.equal(f.dialogs.length,0);assert.equal(f.started.length,1);assert.equal(f.started[0][4],undefined);
  }
});

test('approval cannot outlive its connection, service identity, local network preference, visibility or the selected meeting',async()=>{
  for(const change of [f=>f.state.generation++,f=>f.state.scope='another',f=>f.state.service='Different',f=>f.page.meetingSpeech.extensionRef='new/asr',
    f=>f.page.meetingSpeech.ready=false,f=>f.network.wifiOnly=false,f=>f.page.visible=false,f=>f.page.setMeetingKind('note'),
    f=>f.page.meetingTitle='Different',f=>f.page.privateMeeting=true,f=>f.owner.profile.pin='different',f=>f.page.resetMeetingMode()]){
    const f=fixture();f.page.chooseMode(true);f.page.requestRecordingStart();change(f);f.dialogs[0].secondaryButton.action();await settle();
    assert.deepEqual(f.started,[]);
  }
  const f=fixture();let resume;f.page.loadRecordings=()=>new Promise(done=>resume=done);
  f.page.chooseMode(true);f.page.requestRecordingStart();f.dialogs[0].secondaryButton.action();
  f.state.service='Different';resume(true);await settle();assert.deepEqual(f.started,[]);assert.equal(f.page.recording.error,'request_updated');
});

test('the picker, privacy statements and status labels resolve in all four shipped resource configurations',()=>{
  for(const locale of ['base','en_US','zh_CN','ja_JP']){
    const words=new Map(JSON.parse(readFileSync(new URL('../entry/src/main/resources/'+locale+'/element/string.json',import.meta.url))).string.map(s=>[s.name,s.value]));
    for(const phase of ['negotiating','connecting','live','reconnecting','waiting_network','fallback','finishing','final','failed','revoked','previous'])
      assert.ok(words.has('meeting_audio_'+phase),locale+' '+phase);
    for(const key of ['meeting_cloud_service','meeting_start_privacy_cloud','meeting_start_privacy_cloud_days'])
      assert.ok(words.get(key).includes('{service}'),locale+' '+key);
    for(const key of ['meeting_start_privacy_cloud_days','meeting_start_privacy_local_days'])assert.ok(words.get(key).includes('{days}'));
    assert.ok(words.has('meeting_configured_service'));
  }
});
