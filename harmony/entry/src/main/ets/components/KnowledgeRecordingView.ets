import type { MeetingAsk } from '../models/MeetingRoom';
import { Symbols } from '../design/Symbols';
import { Colors } from '../design/Colors';
import { KnowledgeRecording } from '../models/KnowledgeRecording';
import type { KnowledgeRecordingEntry } from '../models/KnowledgeRecording';
import { Tokens } from '../design/Tokens';
import { tr, trError } from '../design/Strings';

/** Capture controls for the local recording owned by the current Knowledge Hub/manager. */
@ComponentV2
export struct KnowledgeRecordingView {
  @Param startKind: string = 'note';
  @Param startTitle: string = '';
  @Param roomRole:string='off';
  @Param privateMark:boolean=false;
  @Local roomQuestion:string='';
  @Require @Param model: KnowledgeRecording;
  @Event onDeleteLocal: () => void = () => {};

  @Local elapsed: number = 0;
  private tick: number = -1;
  aboutToAppear(): void { this.updateClock(); }
  @Monitor('model.captureState','model.entry') updateClock(): void {
    if (this.tick !== -1) clearInterval(this.tick);
    this.tick = -1;
    const start = this.model.entry ? Date.parse(this.model.entry.startedAt) : 0;
    if (this.model.captureState !== 'recording' || !Number.isFinite(start) || !start) {
      this.elapsed = this.model.entry?.duration || 0; return;
    }
    const update = ():void => { this.elapsed = Math.max(0,(Date.now() - start) / 1000); };
    update(); this.tick = setInterval(update,1000);
  }
  private duration(): string {
    const n = Math.floor(this.elapsed);
    return String(Math.floor(n/60)).padStart(2,'0') + ':' + String(n%60).padStart(2,'0');
  }
  aboutToDisappear(): void {
    if (this.tick !== -1) clearInterval(this.tick);
    this.tick = -1;
    this.model.stopForBackground().catch(() => undefined);
  }

  private statusKey(): string {
    if (this.model.captureState === 'requesting') return 'knowledge_recording_requesting';
    if (this.model.captureState === 'recording') return 'knowledge_recording_active';
    if (this.model.transcriptionState === 'transcribing') return 'knowledge_recording_transcribing';
    if (this.model.transcriptionState === 'complete') {
      if (this.model.uploadState === 'uploading') return 'knowledge_recording_uploading';
      if (this.model.uploadState === 'stored') return 'knowledge_recording_uploaded';
      if (this.model.uploadState === 'unknown') return 'knowledge_recording_upload_unknown';
      if (this.model.uploadState === 'failed') return 'knowledge_recording_upload_failed';
      if (this.model.uploadState === 'deleted') return 'knowledge_recording_deleted';
      if (this.model.uploadState === 'stale') return 'knowledge_recording_stale';
      if (this.model.uploadState === 'expired') return 'knowledge_recording_expired';
      return 'knowledge_recording_transcribed';
    }
    if (this.model.captureState === 'stopped') return 'knowledge_recording_stopped';
    return 'knowledge_recording_capture_ready';
  }

  private transcribeLabel(): string {
    return this.model.transcriptionState === 'failed' ? tr('knowledge_recording_retry_transcribe') : tr('knowledge_recording_transcribe');
  }

  private confirmTranscription(): void {
    const owner = this.model, entry = owner.entry;
    if (!entry || !owner.canTranscribe()) return;
    this.getUIContext().showAlertDialog({ title: this.transcribeLabel(), message: tr('knowledge_recording_save_notice'),
      primaryButton: { value: tr('cancel'), action: () => {} },
      secondaryButton: { value: tr('confirm'), action: () => {
        if (this.model === owner && owner.entry === entry && owner.canTranscribe()) owner.transcribeAndUpload().catch(() => false);
      } } });
  }

  private recordingOptions(): SelectOption[] {
    return this.model.entries.map((entry: KnowledgeRecordingEntry): SelectOption => ({
      value: entry.title || new Date(entry.startedAt).toLocaleString()
    }));
  }

  private recordingIndex(): number {
    const selectedId = this.model.entry?.id || '';
    const index = this.model.entries.findIndex((entry: KnowledgeRecordingEntry) => entry.id === selectedId);
    return index >= 0 ? index : 0;
  }

  @Builder meetingRoom() {
    Column({space:8}) {
      Text(tr('phone_meeting_room')).fontSize(18).maxFontScale(Tokens.interfaceScaleFor(18)).fontWeight(FontWeight.Medium).fontColor(Colors.ink)
      Text(tr(this.model.room.state==='ended' ? 'phone_meeting_closed' : this.model.room.state==='unavailable' ? 'phone_meeting_unavailable' :
        this.model.room.state==='ready' ? 'phone_meeting_live_ready' : 'phone_meeting_live_starting')).fontSize(14).maxFontScale(Tokens.interfaceScaleFor(14)).fontColor(Colors.secondary)
      if (this.model.roomSpeechProblem) Text(tr(this.model.roomSpeechProblem)).fontSize(14).maxFontScale(Tokens.interfaceScaleFor(14)).fontColor(Colors.secondary)
      if (this.model.room.problem) Row({space:8}) {
        Text(tr(this.model.room.problem)).fontSize(14).maxFontScale(Tokens.interfaceScaleFor(14)).fontColor(Colors.secondary).layoutWeight(1)
        Button(tr('retry'),{buttonStyle:ButtonStyleMode.TEXTUAL}).maxFontScale(Tokens.interfaceFontMaxScale).fontColor(Colors.link).onClick(() => this.model.room.sync())
      }.width('100%')
      if (this.model.room.latest) Text(this.model.room.latest).fontSize(16).fontColor(Colors.ink).width('100%').id('recordings.room.line')
      ForEach(this.model.room.shownAsks,(ask:MeetingAsk) => {
        Column({space:4}) {
          Text(ask.question).fontSize(15).fontWeight(FontWeight.Medium).fontColor(Colors.ink)
          Text(ask.answer || tr(ask.state==='failed' ? 'phone_meeting_no_answer' : 'pending')).fontSize(15).fontColor(Colors.secondary)
        }.width('100%').alignItems(HorizontalAlign.Start).id('recordings.room.answer.'+ask.id)
      },(ask:MeetingAsk)=>JSON.stringify([ask.id,ask.state,ask.answer]))
      if (this.model.captureState==='recording') Row({space:6}) {
        TextInput({text:this.roomQuestion,placeholder:tr('phone_meeting_question')}).layoutWeight(1)
          .onChange((v:string)=>this.roomQuestion=v).id('recordings.room.question')
        Button(tr('send'),{buttonStyle:ButtonStyleMode.TEXTUAL}).maxFontScale(Tokens.interfaceFontMaxScale).enabled(!!this.roomQuestion.trim()).fontColor(Colors.link)
          .onClick(() => { const value=this.roomQuestion; this.roomQuestion=''; this.model.askRoom(value).then((ok:boolean) => { if (!ok && !this.roomQuestion) this.roomQuestion=value; }); })
          .id('recordings.room.ask')
      }.width('100%')
    }.width('100%').alignItems(HorizontalAlign.Start).padding(15).backgroundColor(Colors.surface).borderRadius(15).id('recordings.room')
  }
  build() {
    Column({space:10}) {
      Column({space:8}) {
        Row({space:8}) {
          SymbolGlyph($r('sys.symbol.mic_fill')).fontSize(19).fontColor([Colors.danger])
          Text(tr(this.statusKey())).fontSize(15).maxFontScale(Tokens.interfaceScaleFor(15)).fontWeight(FontWeight.Medium).fontColor(Colors.ink).layoutWeight(1)
            .id('knowledge.recording.status')
          if (this.model.captureState === 'requesting' || this.model.transcriptionState === 'transcribing' || this.model.uploadState === 'uploading')
            LoadingProgress().width(20).height(20)
        }.width('100%')
        if (this.model.entry) {
          Text(this.model.entry.title || new Date(this.model.entry.startedAt).toLocaleString()).fontSize(18).maxFontScale(Tokens.interfaceScaleFor(18)).fontWeight(FontWeight.Medium).fontColor(Colors.ink).maxLines(2)
        }
        Text(this.duration()).fontSize(32).maxFontScale(Tokens.interfaceScaleFor(32)).fontWeight(FontWeight.Bold).fontColor(Colors.ink).id('knowledge.recording.duration')
        if (this.model.canStop()) Button(tr('knowledge_recording_stop')).maxFontScale(Tokens.interfaceFontMaxScale).width('100%').height(43).fontColor(Color.White).backgroundColor(Colors.danger)
          .onClick(() => this.model.stop().catch(() => false)).id('knowledge.recording.stop')
        else Button(tr('phone_record_start')).maxFontScale(Tokens.interfaceFontMaxScale).width('100%').height(43).fontColor(Color.White).backgroundColor(Colors.danger)
          .enabled(this.model.canStart()).onClick(() => this.model.start(this.startKind,this.startTitle,this.roomRole,this.privateMark).catch(() => false)).id('knowledge.recording.start')
        Text(tr('phone_record_audio_local')).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).fontColor(Colors.secondary).width('100%')
      }.width('100%').alignItems(HorizontalAlign.Start).padding(15).backgroundColor(Colors.surface).borderRadius(15)
      if (this.model.room.active) this.meetingRoom()
      if (this.model.entries.length > 1) Select(this.recordingOptions()).selected(this.recordingIndex())
        .enabled(this.model.canSelectEntry()).onSelect((i:number) => {
          const selected = this.model.entries[i]; if (selected) this.model.selectEntry(selected.id);
        }).width('100%').id('knowledge.recording.select')
      if (this.model.error) Text(trError(this.model.error)).fontSize(14).maxFontScale(Tokens.interfaceScaleFor(14)).fontColor(Colors.danger).width('100%').id('knowledge.recording.error')
      if (this.model.entry && this.model.captureState === 'stopped') Column({space:4}) {
        if (this.model.canPlay()) Button({type:ButtonType.Normal}) {
          Row({space:9}) {
            SymbolGlyph(this.model.playingId ? Symbols.stop : $r('sys.symbol.play_fill')).fontSize(17).fontColor([Colors.link])
            Text(tr(this.model.playingId ? 'stop' : 'phone_record_play')).fontSize(16).maxFontScale(Tokens.interfaceScaleFor(16)).fontColor(Colors.ink).layoutWeight(1)
          }.width('100%').height(44)
        }.padding({left:12,right:12}).width('100%').backgroundColor(Colors.surface)
          .onClick(() => this.model.playSelected().catch(() => false)).id('knowledge.recording.play')
        if (this.model.canTranscribe()) Button(this.transcribeLabel(),{buttonStyle:ButtonStyleMode.TEXTUAL}).fontSize(16).maxFontScale(Tokens.interfaceScaleFor(16))
          .width('100%').enabled(this.model.canTranscribe()).onClick(() => this.confirmTranscription()).id('knowledge.recording.transcribe')
        if (this.model.transcriptionState === 'transcribing') Button(tr('cancel'),{buttonStyle:ButtonStyleMode.TEXTUAL}).maxFontScale(Tokens.interfaceFontMaxScale)
          .onClick(() => this.model.cancelTranscription().catch(() => undefined)).id('knowledge.recording.cancel')
        if (this.model.canRetryUpload()) Button(tr('knowledge_recording_retry_upload'),{buttonStyle:ButtonStyleMode.TEXTUAL}).maxFontScale(Tokens.interfaceFontMaxScale)
          .onClick(() => this.model.retryUpload().catch(() => false)).id('knowledge.recording.retry_upload')
      }.width('100%').padding(6).backgroundColor(Colors.surface).borderRadius(13)
      if (this.model.entry?.transcript) Column({space:6}) {
        Text(tr('knowledge_transcript')).fontSize(15).maxFontScale(Tokens.interfaceScaleFor(15)).fontWeight(FontWeight.Medium).fontColor(Colors.secondary)
        Text(this.model.entry!.transcript).fontSize(17).fontColor(Colors.ink).copyOption(CopyOptions.LocalDevice).id('knowledge.recording.transcript')
      }.width('100%').alignItems(HorizontalAlign.Start).padding(13).backgroundColor(Colors.surface).borderRadius(13)
      if (this.model.canDeleteLocal()) Button(tr('knowledge_recording_delete_local'),{buttonStyle:ButtonStyleMode.TEXTUAL}).maxFontScale(Tokens.interfaceFontMaxScale).fontColor(Colors.danger)
        .onClick(() => this.onDeleteLocal()).id('knowledge.recording.delete_local')
    }.padding({left:16,right:16,top:8,bottom:18}).width('100%').alignItems(HorizontalAlign.Start)
  }
}
