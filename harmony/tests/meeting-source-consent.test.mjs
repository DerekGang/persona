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
const {PhoneMeetingOptions,meetingCaptureHeight,meetingDefaultTitle,meetingPlaceholderVp}=await import('../entry/src/main/ets/models/PhoneMeetingOptions.ets');
const {MeetingHubAudio,MeetingHubChoice}=await import('../entry/src/main/ets/models/MeetingHubAudio.ets');
const source=readFileSync(new URL('../entry/src/main/ets/components/KnowledgeWorkspacePage.ets',import.meta.url),'utf8');
const recording=readFileSync(new URL('../entry/src/main/ets/components/KnowledgeRecordingView.ets',import.meta.url),'utf8');
const strings=new Map(JSON.parse(readFileSync(new URL('../entry/src/main/resources/en_US/element/string.json',import.meta.url))).string.map(s=>[s.name,s.value]));
const tr=key=>strings.get(key) || key;
const methods=source.slice(source.indexOf('  private resetMeetingMode()'),source.indexOf('  @Local meetingTitle:'))+
  source.slice(source.indexOf('  private requestRecordingStart()'),source.indexOf('  private async readMeetingCalendar('));
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
    options:()=>({selectedInput:-1}),defaultRecordingTitle:()=>'Synthetic draft',
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

test('the meeting shows one identified transcript, choosing the device or hub that actually produced each line',()=>{
  const methods=recording.slice(recording.indexOf('  private hasHubTranscription('),recording.indexOf('  @Builder hubTranscription('));
  const View=new Function('return '+stripTypeScriptTypes('class SourceView {'+methods+'}'))();
  const view=new View(),audio=new MeetingHubAudio();
  view.model={hubAudio:audio,room:{latest:'Phone sentence'},entry:{roomRole:'listener',askedTranscription:'hub'}};
  audio.phase='live';audio.latestLine='Hub sentence';audio.caption='Temporary hub words';
  assert.equal(view.currentLine(),'Hub sentence');assert.equal(view.currentCaption(),'Temporary hub words');assert.equal(view.phoneLines(),false);
  audio.problem='no_interim';assert.equal(view.currentLine(),'Hub sentence');assert.equal(view.currentCaption(),'');
  audio.problem='upstream_unavailable';assert.equal(view.currentLine(),'Phone sentence');assert.equal(view.currentCaption(),'');
  audio.problem='';assert.equal(view.currentCaption(),'Temporary hub words');
  view.model.entry.roomRole='off';assert.equal(view.currentLine(),'Phone sentence');assert.equal(view.currentCaption(),'Temporary hub words');
  view.model.entry.roomRole='participant';
  for(const phase of ['waiting_network','reconnecting','fallback','failed','revoked']){
    audio.phase=phase;assert.equal(view.currentLine(),'Phone sentence');assert.equal(view.currentCaption(),'');
  }
  audio.phase='final';assert.equal(view.currentLine(),'Phone sentence');assert.equal(view.currentCaption(),'');
  assert.match(recording,/!this\.hasHubTranscription\(\) && this\.model\.room\.latest/);
  assert.match(recording,/this\.model\.room\.active && this\.model\.entry\?\.roomRole!=='off'/);
});

test('the title uses the mobile date and minutes for the real locale and only its placeholder takes the compact text size',()=>{
  const time=new Date(2026,10,22,21,9);
  const zh=meetingDefaultTitle('meeting',time,'zh-CN','会议','笔记');
  assert.match(zh,/会议 · 2026年11月22日 21:09/);
  assert.match(meetingDefaultTitle('note',time,'en-US','Meeting','Note'),/Note · Nov 22, 2026 9:09 PM/);
  assert.match(meetingDefaultTitle('meeting',time,'ja-JP','会議','ノート'),/^会議 · .*2026.*11.*22.*21:09/);
  const title=source.slice(source.indexOf('TextInput({text:this.meetingTitle'),source.indexOf(".id('recordings.title')"));
  assert.match(title,/placeholderFont\(\{size:this.placeholderSize\(\)/);
  assert.match(title,/onAreaChange[^\n]*recordingTitleWidth=Number\(next.width\)/);
  assert.equal(meetingPlaceholderVp(326,375,23),'19.2vp');
  assert.equal(meetingPlaceholderVp(380,335,23),'23vp');
  assert.ok(parseFloat(meetingPlaceholderVp(258,375,23))*375/23<=246);
  assert.doesNotMatch(title,/\.maxFontScale/);
});

test('the meeting start action scales its real height and gives a short form room to scroll',()=>{
  assert.equal(meetingCaptureHeight(1,700),58);
  assert.ok(Math.abs(meetingCaptureHeight(3.2,700)-71.8)<.02);
  assert.equal(meetingCaptureHeight(3.2,143),55);
  assert.equal(meetingCaptureHeight(3.2,115),44);
  assert.ok(meetingCaptureHeight(1.2,700)>58);
});

test('an idle meeting menu only offers the offline language and physical inputs reported for its own visible draft',async()=>{
  const form=new PhoneMeetingOptions(),old={},next={},marks=[];
  let active=true,resolve,inputs=[{id:11,bluetooth:false,name:''},{id:21,bluetooth:true,name:'Synthetic headset'}];
  form.bind(old,true,true,false,()=>active,value=>marks.push(value),()=>new Promise(done=>resolve=done),()=>inputs);
  form.togglePrivate();form.togglePrivate();assert.deepEqual(marks,[true,false]);
  form.selectInput(21);assert.equal(form.selectedInput,21);inputs=[];await form.prepare();assert.equal(form.selectedInput,-1);
  form.clear(old);form.bind(next,true,false,false,()=>active,()=>assert.fail(),async()=>['zh-CN'],()=>[]);
  resolve(['en-US','ja-JP']);await settle();assert.equal(form.languageAvailable,true);assert.deepEqual(form.inputs,[]);
  form.togglePrivate();assert.equal(form.marked,false);
  active=false;form.selectInput(21);assert.equal(form.selectedInput,-1);
  form.clear(next);assert.equal(form.available,false);assert.equal(form.languageAvailable,false);
});

test('the native recording header uses interface body type while its full profile remains a reading surface',()=>{
  const detail=source.slice(source.indexOf('  @Builder recordingDetail()'),source.indexOf(".id('knowledge.recording.detail')"));
  assert.match(detail,/Text\(this\.workspace\.selected\.detail\)\.fontSize\(Tokens\.body\)\s*\.maxFontScale\(Tokens\.interfaceScaleFor\(Tokens\.body\)\)/);
  assert.match(detail,/MarkdownText\(\{text:Wire\.text\(this\.workspace\.detail,'summary'\)/);
  assert.match(detail,/\.id\('knowledge.recording.intro'\)/);
});


test('the recording detail keeps its actual path explanation and its minutes entry alongside the source-bound reading profile',()=>{
  const block=source.slice(source.indexOf('  @Builder recordingDetail()'),source.indexOf(".id('knowledge.recording.detail')"));
  assert.match(block,/if \(Wire\.text\(this\.workspace\.detail,'path'\)\) \{[\s\S]*?recordingFootnote\('recording_detail_folder_access'\)/);
  assert.match(block,/MarkdownText\(\{text:Wire\.text\(this\.workspace\.detail,'summary'\),recapReading:true/);
  assert.match(block,/\.id\('knowledge\.recording\.tools'\)/);
  const messages=['base','en_US','ja_JP','zh_CN'].map(locale=>JSON.parse(readFileSync(new URL('../entry/src/main/resources/'+locale+'/element/recording_detail.json',import.meta.url),'utf8')).string);
  for (const entries of messages) assert.ok(entries.find(item=>item.name==='recording_detail_folder_access')?.value.length>16);
});
