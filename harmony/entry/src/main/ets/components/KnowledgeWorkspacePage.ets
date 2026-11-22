import { IconActions } from '../design/IconActions';
import { Symbols } from '../design/Symbols';
import { PhoneSectionTitle } from '../design/PhoneStyle';
import type { KnowledgeRecordingEntry } from '../models/KnowledgeRecording';
import { Colors } from '../design/Colors';
import { ButtonStyles, SelectStyles } from '../design/Controls';
import { ClientModel } from '../models/ClientModel';
import { KnowledgeDirectory, KnowledgeListEntry, KnowledgeSearchHit, KnowledgeWorkspace, RecordingFactsDraft } from '../models/KnowledgeWorkspace';
import { ResourceRow, Wire } from '../models/Wire';
import { BoundRow, reconcileBoundRows } from '../models/RowBinding';
import { MarkdownText } from './MarkdownText';
import { Tokens } from '../design/Tokens';
import { tr } from '../design/Strings';
import { common } from '@kit.AbilityKit';
import { nativeMeetingServices } from '../platform/NativeMeetingServices';
import { MeetingOccurrence } from '../models/MeetingCalendarReminders';
import { MeetingPreferences, meetingRoleLabel } from '../models/MeetingPreferences';
import { KnowledgeRecording } from '../models/KnowledgeRecording';
import { NativeRecording } from '../platform/NativeRecording';
import { KnowledgeRecordingView } from './KnowledgeRecordingView';
import { LengthMetrics, UIUtils } from '@kit.ArkUI';
import { nativeEnvironment } from '../platform/NativeEnvironment';
import type { Binding } from '@kit.ArkUI';

const KB_KINDS: string[] = ['projects', 'topics', 'chats', 'people', 'docs', 'meetings', 'events'];

@ComponentV2
struct KnowledgeLibraryRow {
  @Require @Param row: ResourceRow;
  @Require @Param symbol: Resource;
  @Event open: () => void = () => {};
  @Computed get entry(): KnowledgeListEntry { return new KnowledgeListEntry(this.row); }

  private dateLabel(): string {
    const stamp = Date.parse(this.entry.updated);
    if (!Number.isFinite(stamp)) return '';
    const minutes = Math.floor((Date.now()-stamp)/60000);
    if (minutes >= 0 && minutes < 1440)
      return minutes < 1 ? tr('agent_time_just_now') : minutes < 60 ? String(minutes)+tr('agent_time_minute_suffix') :
        String(Math.floor(minutes/60))+tr('agent_time_hour_suffix');
    return new Date(stamp).toLocaleDateString();
  }
  private tint(): ResourceColor {
    if (this.entry.priority === 'P0' || this.entry.priority === 'P2') return Colors.link;
    return this.entry.priority === 'P1' ? Colors.working : Colors.secondary;
  }
  @Builder badge(value: string, tint: ResourceColor) {
    Text(value).fontSize(11).maxFontScale(Tokens.interfaceScaleFor(11)).fontColor(tint).padding({left:5,right:5,top:1,bottom:1})
      .borderRadius(6).backgroundColor(Colors.control)
  }
  build() {
    Row({space:10}) {
      Stack() {
        Rect().width(36).height(36).radius(10).fill(this.tint()).opacity(0.12)
        SymbolGlyph(this.symbol).fontSize('17vp').fontColor([this.tint()])
      }.width(36).height(36).flexShrink(0)
      Column({space:3}) {
        Row({space:6}) {
          Text(this.row.title || this.row.id).fontSize(17).maxFontScale(Tokens.interfaceFontMaxScale).fontWeight(FontWeight.Medium).fontColor(Colors.ink).layoutWeight(1).maxLines(2)
          if (this.dateLabel())
            Text(this.dateLabel()).fontSize(11).maxFontScale(Tokens.interfaceScaleFor(11)).fontColor(Colors.secondary).maxLines(1)
        }.width('100%').alignItems(VerticalAlign.Top)
        if (this.entry.summary) Text(this.entry.summary).fontSize(14).maxFontScale(Tokens.interfaceScaleFor(14)).fontColor(Colors.secondary).maxLines(2)
          .textOverflow({overflow:TextOverflow.Ellipsis})
        Flex({wrap:FlexWrap.Wrap,space:{main:LengthMetrics.vp(4),cross:LengthMetrics.vp(3)}}) {
          if (this.entry.priority) this.badge(this.entry.priority, this.tint())
          if (this.entry.duration) this.badge(tr(this.entry.duration==='long' ? 'knowledge_project_long' : 'knowledge_project_short'),Colors.secondary)
          if (this.entry.relevance>0) this.badge(tr('knowledge_relevance')+' '+String(this.entry.relevance), Colors.link)
          ForEach(this.entry.tags.slice(0,2),(tag:string) => {
            Text('#'+tag).fontSize(11).maxFontScale(Tokens.interfaceScaleFor(11)).fontColor(Colors.secondary).maxLines(1)
          },(tag:string) => tag)
        }.width('100%')
      }.layoutWeight(1).alignItems(HorizontalAlign.Start)
      SymbolGlyph(Symbols.expand).fontSize('12vp').fontColor([Colors.secondary]).alignSelf(ItemAlign.Center)
    }.width('100%').alignItems(VerticalAlign.Top).padding({left:14,right:12,top:12,bottom:12}).constraintSize({minHeight:52})
      .backgroundColor(Colors.surface).accessibilityGroup(true).onClick(() => this.open()).id('knowledge.entry.'+this.row.path+'.'+this.row.id)
  }
}

@ComponentV2
export struct KnowledgeWorkspacePage {
  @Local dispatchExpanded: boolean = false;
  @Local facts: RecordingFactsDraft = new RecordingFactsDraft();
  @Local recordingTools: boolean = false;
  private kindScroller: Scroller = new Scroller();
  private kindListMounted: boolean = false;
  @Require @Param model: ClientModel;
  @Param row: ResourceRow | undefined = undefined;
  @Param list: ResourceRow[] = [];
  @Param visible: boolean = true;
  @Param rail: boolean = false;
  @Param initialKind: string = '';
  @Param editRevision: number = 0;
  @Local metadataSheet: boolean = false;
  @Local localMeetings: boolean = false;
  @Local meetingKind: string = 'meeting';
  @Local meetingPreferences:MeetingPreferences = new MeetingPreferences();
  @Local invitationOverride:string='';
  @Local privateMeeting:boolean=false;
  private canInvite():boolean { return this.model.connected && this.model.features.includes('meeting_attendance') && this.model.features.includes('meeting_conversation'); }
  private roomRole():string {
    if (this.meetingKind!=='meeting' || !this.canInvite() || this.invitationOverride==='off') return 'off';
    if (!this.invitationOverride) return this.meetingPreferences.role;
    return this.meetingPreferences.role==='participant' ? 'participant' : 'listener';
  }
  @Monitor('model.features') meetingFeaturesChanged():void {
    if (this.pageAvailable && this.initialKind==='recordings' && !this.row) this.meetingPreferences.open(this.model);
  }

  @Local meetingTitle: string = '';
  @Local calendarEvents:MeetingOccurrence[]=[];
  private selectedCalendarEvent:string='';
  private recordingCalendarEvent:string='';
  @Monitor('recording.captureState') meetingCaptureChanged():void {
    if (this.recordingCalendarEvent && this.recording.captureState!=='recording') { this.recordingCalendarEvent='';nativeMeetingServices.setRecordingEvent(''); }
  }
  @Local calendarState: string = '';
  private calendarRequest: number = 0;
  @Local showIgnored: boolean = false;
  @Local refreshing: boolean = false;
  @Local filterWidth:number=0;
  private separateTagLine():boolean {
    return this.filterWidth>0 && this.filterWidth<215*Math.min(nativeEnvironment.fontScale,Tokens.interfaceScaleFor(14));
  }
  @Event onAllMeetings: () => void = () => { this.localMeetings = true; this.workspace.setKind('meetings'); };
  private localOwner: string = '';
  private localOpening: Promise<void> | undefined;
  @Event onOpenResource: (row: ResourceRow) => void = (_row: ResourceRow) => {};
  @Event onChat: (row: ResourceRow) => void = (_row: ResourceRow) => {};
  @Event onLink: (url: string) => void = (_url: string) => {};
  @Local workspace: KnowledgeWorkspace = new KnowledgeWorkspace();
  @Local entryRowBindings: BoundRow<ResourceRow>[] = [];
  @Local resultRowBindings: BoundRow<KnowledgeSearchHit>[] = [];
  @Local agentRowBindings: BoundRow<ResourceRow>[] = [];
  private rowBindingOwner: Object | undefined;
  private rowBindingScope: string = '';
  @Local momentText: string = '';
  @Local selectedActions: string[] = [];
  @Local recordingOpen: boolean = false;
  @Local recording: KnowledgeRecording = new KnowledgeRecording(new NativeRecording());
  private lastVisible: boolean = true;
  private workspaceOpening: boolean = false;
  private queuedRefresh: boolean = false;
  private openSerial: number = 0;
  private pageAvailable: boolean = false;

  aboutToAppear(): void {
    this.pageAvailable = true; this.meetingFeaturesChanged(); this.lastVisible = this.visible; this.syncRowBindings(); this.openWorkspace();
    if (this.initialKind === 'recordings' && !this.row) this.loadRecordings().catch(() => undefined);
  }
  aboutToDisappear(): void {
    this.pageAvailable = false; this.meetingPreferences.close(); this.calendarRequest++; this.localOwner = ''; this.localOpening = undefined; this.openSerial++; this.workspaceOpening = false; this.queuedRefresh = false;
    this.workspace.hide(); this.recording.dispose().catch(() => undefined);
  }

  @Monitor('editRevision') editTagsRequested(): void {
    if (!this.pageAvailable || !this.visible || this.row?.path!=='recordings' || this.workspace.selected.id!==this.row.id) return;
    this.workspace.beginEdit();
    this.metadataSheet=this.workspace.editOpen;
  }
  @Monitor('workspace.editOpen') metadataChanged():void { if (!this.workspace.editOpen) this.metadataSheet=false; }
  @Monitor('row') rowChanged(): void { this.metadataSheet=false; this.facts.close(); this.recordingTools=false; this.syncRowBindings(); this.openWorkspace(); }
  @Monitor('workspace.kind') centerKind(): void {
    const index=KB_KINDS.indexOf(this.workspace.kind);
    if (index>=0 && this.kindListMounted) this.kindScroller.scrollToIndex(index,false,ScrollAlign.CENTER);
  }
  @Monitor('model.profile') profileChanged(): void {
    this.metadataSheet=false; this.facts.close(); this.recordingTools=false; this.showIgnored=false; this.recordingOpen = false;
    this.selectedCalendarEvent=''; this.calendarRequest++; this.calendarEvents=[]; this.calendarState=''; this.meetingTitle=''; this.meetingKind='meeting';
    this.privateMeeting=false; this.invitationOverride=''; this.meetingFeaturesChanged(); this.localMeetings = false; this.localOwner = ''; this.localOpening = undefined;
    this.recording.stopForBackground().catch(() => undefined); this.syncRowBindings(); this.openWorkspace();
    if (this.initialKind === 'recordings' && !this.row) this.loadRecordings().catch(() => undefined);
  }
  @Monitor('model.connected') connectionChanged(): void { if (this.model.connected) { this.refreshWhenReady(); if (this.recordingOpen || this.initialKind === 'recordings') this.loadRecordings().catch(() => undefined); } }
  @Monitor('visible') visibilityChanged(): void {
    const becameVisible = !this.lastVisible && this.visible;
    this.lastVisible = this.visible;
    if (becameVisible && this.pageAvailable && !this.row) this.refreshWhenReady();
  }
  @Monitor('workspace.entries', 'workspace.results', 'workspace.agents') rowSourcesChanged(): void { this.syncRowBindings(); }

  private openWorkspace(): void {
    const serial = ++this.openSerial;
    this.workspaceOpening = true;
    this.workspace.open(this.model, this.row, this.list, this.initialKind).catch(() => undefined).finally(() => {
      if (serial !== this.openSerial) return;
      this.workspaceOpening = false;
      const shouldRefresh = this.queuedRefresh; this.queuedRefresh = false;
      if (shouldRefresh && this.pageAvailable) this.workspace.refresh().catch(() => undefined);
    });
  }

  private refreshWhenReady(): void {
    if (!this.pageAvailable) return;
    if (this.workspaceOpening) { this.queuedRefresh = true; return; }
    this.workspace.refresh().catch(() => undefined);
  }

  private syncRowBindings(): void {
    const scope = JSON.stringify([this.model.scope(), this.model.profile.id, this.model.profile.manager, this.row?.key() || 'list']);
    this.entryRowBindings = reconcileBoundRows(this.entryRowBindings, this.workspace.entries, (row: ResourceRow) => row.key(),
      this.rowBindingOwner, this.model, this.rowBindingScope, scope);
    this.resultRowBindings = reconcileBoundRows(this.resultRowBindings, this.workspace.results, (hit: KnowledgeSearchHit) => hit.entry.key(),
      this.rowBindingOwner, this.model, this.rowBindingScope, scope);
    this.agentRowBindings = reconcileBoundRows(this.agentRowBindings, this.workspace.agents, (row: ResourceRow) => row.key(),
      this.rowBindingOwner, this.model, this.rowBindingScope, scope);
    this.rowBindingOwner = this.model; this.rowBindingScope = scope;
  }

  private recordingScope(): string {
    return JSON.stringify([this.model.scope(),this.model.pairingRevision(),this.row?.manager || this.model.profile.manager]);
  }
  private async loadRecordings(): Promise<boolean> {
    const context = this.getUIContext().getHostContext(), key = this.recordingScope();
    if (!this.pageAvailable || !context || this.model.demo || !this.model.profile.id) return false;
    if (key !== this.localOwner) {
      this.localOwner = key;
      this.localOpening = this.recording.open(this.model,this.row?.manager || this.model.profile.manager,context as common.UIAbilityContext);
    }
    const pending = this.localOpening;
    if (pending) {
      try { await pending; } finally { if (this.localOpening === pending) this.localOpening = undefined; }
    }
    return this.pageAvailable && this.visible && key === this.localOwner && this.recordingScope() === key;
  }
  private async openRecording(id: string = '', start: boolean = false): Promise<void> {
    const kind=this.meetingKind, role=this.roomRole(), privateMark=this.privateMeeting;
    const title=this.meetingTitle.trim() || tr(kind==='meeting' ? 'phone_record_meeting' : 'phone_record_note')+' · '+new Date().toLocaleDateString();

    if (!await this.loadRecordings()) return;
    if (id) this.recording.selectEntry(id);
    this.recordingOpen = true;
    const event=kind==='meeting' ? this.selectedCalendarEvent : '';
    if (start && this.recording.canStart() && await this.recording.start(kind,title,role,privateMark)) {
      this.recordingCalendarEvent=event;nativeMeetingServices.setRecordingEvent(event);this.selectedCalendarEvent='';
      this.meetingTitle=''; this.calendarEvents=[]; this.calendarState=''; this.invitationOverride=''; this.privateMeeting=false;
    }
  }

  // Calendar titles stay on this device until the person picks one and explicitly saves a transcript.
  private async readMeetingCalendar(): Promise<void> {
    const context=this.getUIContext().getHostContext() as common.UIAbilityContext, owner=this.recordingScope();
    if (!context || this.calendarState==='loading') return;
    const revision=++this.calendarRequest;
    this.calendarState='loading'; this.calendarEvents=[];
    try {
      const events=await nativeMeetingServices.today(context);
      if (!this.pageAvailable || revision!==this.calendarRequest || owner!==this.recordingScope()) return;
      this.calendarEvents=events;
      this.calendarState=this.calendarEvents.length ? 'ready' : 'phone_meeting_calendar_empty';
    } catch (error) {
      if (this.pageAvailable && revision===this.calendarRequest && owner===this.recordingScope())
        this.calendarState=(error as Error).message==='permission_denied' ? 'permission_denied' : 'failed';
    }
  }

  private deleteRecording(): void {
    const id = this.recording.entry?.id, scope = this.model.scope(), pairing = this.model.pairingRevision();
    this.getUIContext().showAlertDialog({ title: tr('knowledge_recording_delete_local'), message: this.recording.entry?.startedAt || tr('confirm'),
      primaryButton: { value: tr('cancel'), action: (): void => {} },
      secondaryButton: { value: tr('delete'), action: (): void => {
        if (this.recordingOpen && this.model.scope() === scope && this.model.pairingRevision() === pairing && this.recording.entry?.id === id) this.recording.deleteLocal();
      } } });
  }

  @Builder recordingSheet() {
    Scroll() { KnowledgeRecordingView({ model: this.recording,startKind:this.meetingKind,startTitle:this.meetingTitle,roomRole:this.roomRole(),privateMark:this.privateMeeting,onDeleteLocal: (): void => this.deleteRecording() }) }
      .width('100%').height('100%').align(Alignment.TopStart).backgroundColor(Colors.grouped)
  }

  private kindLabel(kind: string): string { return tr('knowledge_kind_' + kind); }
  private kindIcon(kind: string): Resource {
    if (kind === 'projects') return Symbols.folder;
    if (kind === 'chats') return Symbols.persona;
    if (kind === 'people') return Symbols.agents;
    if (kind === 'meetings' || kind === 'events') return Symbols.calendar;
    if (kind === 'recordings') return $r('sys.symbol.mic');
    return kind === 'topics' ? Symbols.apps : Symbols.document;
  }
  private clock(seconds: number): string {
    const whole = Number.isFinite(seconds) ? Math.max(0,Math.floor(seconds)) : 0;
    return String(Math.floor(whole / 60)) + ':' + String(whole % 60).padStart(2,'0');
  }
  private clockTime(ms:number): string {
    const date=new Date(ms);
    return String(date.getHours()).padStart(2,'0')+':'+String(date.getMinutes()).padStart(2,'0');
  }
  private localDate(raw:string): string {
    const date = new Date(raw);
    return Number.isFinite(date.getTime()) ? date.toLocaleDateString() + ' ' + String(date.getHours()).padStart(2,'0') + ':' + String(date.getMinutes()).padStart(2,'0') : '';
  }
  private projectRows(section: string): BoundRow<ResourceRow>[] {
    return this.entryRowBindings.filter((binding: BoundRow<ResourceRow>) => new KnowledgeListEntry(binding.value).section === section);
  }
  private sectionLabel(section: string): string {
    if (section === 'focus') return tr('knowledge_focus');
    if (section === 'active') return tr(this.projectRows('focus').length ? 'knowledge_other_active' : 'knowledge_status_active');
    if (section === 'ignored') return tr('knowledge_not_project');
    return tr('knowledge_status_done');
  }
  private isProjectList(): boolean { return this.workspace.kind === 'projects' && !this.workspace.query.trim(); }

  private visibleLocal(): KnowledgeRecordingEntry[] {
    const q = this.workspace.query.trim().toLowerCase();
    return this.recording.entries.filter((v:KnowledgeRecordingEntry) =>
      v.uploadState !== 'stored' && (!q || [v.title,v.startedAt,v.transcript].join(' ').toLowerCase().includes(q)));
  }

  private tagOptions(): SelectOption[] {
    const options: SelectOption[] = [{ value: tr('knowledge_all_tags') }];
    this.workspace.tags.forEach((tag: string) => options.push({ value: '#' + tag }));
    return options;
  }


  private selectTag(index: number): void { this.workspace.setTag(index === 0 ? '' : this.workspace.tags[index - 1] || ''); }

  @Builder knowledgeFilters() {
    Menu() {
      if (!this.workspace.query.trim()) MenuItemGroup({ header: tr('knowledge_filter_tag') }) {
        ForEach(this.tagOptions(), (option: SelectOption, index: number) => {
          MenuItem({ content: option.value }).selected((this.workspace.tag ? '#' + this.workspace.tag : tr('knowledge_all_tags')) === option.value).selectIcon(true)
            .onClick(() => this.selectTag(index))
        }, (option: SelectOption) => JSON.stringify(option.value))
      }
    }.width(Tokens.popupWidth)
  }

  @Builder searchField(meetings:boolean) {
    Search({value:this.workspace.query,placeholder:tr(meetings ? 'phone_record_search' : 'knowledge_search')}).maxFontScale(Tokens.interfaceFontMaxScale)
      .height(Math.max(38,Math.ceil(24*Math.min(nativeEnvironment.fontScale,Tokens.interfaceScaleFor(17)))+8)).textFont({size:16}).backgroundColor(Colors.control)
      .onChange((value:string) => this.workspace.setQuery(value)).id('knowledge.search')
  }
  @Builder libraryRow(row: ResourceRow) {
    KnowledgeLibraryRow({row:row, symbol:this.kindIcon(row.path), open:():void => {
      this.workspace.openEntry(row).then((next:ResourceRow) => this.onOpenResource(next)).catch(() => undefined);
    }})
  }
  @Builder searchHit(hit:KnowledgeSearchHit) {
    Column({space:4}) {
      Text(hit.entry.title || hit.entry.id).fontSize(17).maxFontScale(Tokens.interfaceFontMaxScale).fontWeight(FontWeight.Medium).fontColor(Colors.ink).maxLines(2)
      Text(this.kindLabel(hit.entry.path) + ' · ' + String(hit.count)).fontSize(12).maxFontScale(Tokens.interfaceScaleFor(12)).fontColor(Colors.secondary)
      ForEach(hit.hits.slice(0,2), (line:string,index:number) => Text(line).fontSize(14).maxFontScale(Tokens.interfaceScaleFor(14)).fontColor(Colors.secondary)
        .maxLines(2).textOverflow({overflow:TextOverflow.Ellipsis}), (line:string,index:number) => JSON.stringify([index, line]))
    }.width('100%').padding(13).alignItems(HorizontalAlign.Start).backgroundColor(Colors.surface).borderRadius(12)
      .onClick(() => this.workspace.openEntry(hit.entry).then((next:ResourceRow) => this.onOpenResource(next)).catch(() => undefined))
      .id('knowledge.hit.' + hit.entry.path + '.' + hit.entry.id)
  }
  @Builder sectionEntries() {
    if (this.workspace.query.trim()) {
      ForEach(this.resultRowBindings, (binding:BoundRow<KnowledgeSearchHit>) => {
        ListItem() { this.searchHit(binding.value) }
      }, (binding:BoundRow<KnowledgeSearchHit>) => binding.key)
    } else {
      ForEach(this.entryRowBindings, (binding:BoundRow<ResourceRow>) => {
        ListItem() { this.libraryRow(binding.value) }
      }, (binding:BoundRow<ResourceRow>) => binding.key)
    }
  }
  @Builder listStatus() {
    if (this.workspace.error) ListItem() {
      Row({space:8}) {
        Text(tr(this.workspace.error)).fontSize(14).maxFontScale(Tokens.interfaceScaleFor(14)).fontColor(Colors.secondary).layoutWeight(1)
        Button(tr('retry'),{buttonStyle:ButtonStyleMode.TEXTUAL}).maxFontScale(Tokens.interfaceFontMaxScale).fontColor(Colors.link).onClick(() => this.workspace.refresh()).id('knowledge.retry')
      }.width('100%').padding(10)
    }
    if (this.workspace.loading && !this.workspace.entries.length && !this.workspace.results.length)
      ListItem() { LoadingProgress().width(26).height(26).margin(10) }
    if (!this.workspace.loading && !this.workspace.error && (this.initialKind !== 'recordings' || !this.visibleLocal().length) && (this.workspace.query.trim() ? !this.workspace.results.length : !this.workspace.entries.length))
      ListItem() { Text(tr(this.workspace.query.trim() ? 'knowledge_no_results' : this.workspace.kind === 'recordings' ? 'phone_read_only_recording' : 'knowledge_empty')).fontSize(15).maxFontScale(Tokens.interfaceScaleFor(15)).fontColor(Colors.secondary).padding(10) }
  }
  @Builder kindChip(kind:string) {
    Button({type:ButtonType.Capsule}) {
      Row({space:4}) {
        if (Math.min(nativeEnvironment.fontScale,Tokens.interfaceScaleFor(15))>1.3) SymbolGlyph(this.kindIcon(kind)).fontSize('14vp')
          .fontColor([kind===this.workspace.kind ? Colors.onAction : Colors.ink])
        Text(this.kindLabel(kind)).fontSize(15).maxFontScale(Tokens.interfaceScaleFor(15)).fontWeight(FontWeight.Medium)
          .fontColor(kind===this.workspace.kind ? Colors.onAction : Colors.ink)
      }
    }.constraintSize({minHeight:34}).padding({left:10,right:10,top:4,bottom:4})
      .backgroundColor(kind===this.workspace.kind ? Colors.action : Colors.control)
      .accessibilitySelected(kind===this.workspace.kind).onClick(() => this.workspace.setKind(kind)).id('knowledge.kind.'+kind)
  }
  @Builder kindBar() {
    if (Math.min(nativeEnvironment.fontScale,Tokens.interfaceScaleFor(15))>1.3) {
      Flex({wrap:FlexWrap.Wrap,space:{main:LengthMetrics.vp(6),cross:LengthMetrics.vp(5)}}) {
        ForEach(KB_KINDS,(kind:string) => { this.kindChip(kind) },(kind:string) => kind)
      }.width('100%').padding({top:6,bottom:6}).id('knowledge.kinds')
    } else {
      List({space:6,scroller:this.kindScroller}) {
        ForEach(KB_KINDS,(kind:string) => { ListItem() { this.kindChip(kind) } },(kind:string) => kind)
      }.width('100%').height(48).listDirection(Axis.Horizontal).scrollBar(BarState.Off).alignListItem(ListItemAlign.Center)
        .onAppear(() => { this.kindListMounted=true; })
        .onAreaChange(() => this.centerKind()).onDisAppear(() => { this.kindListMounted=false; }).id('knowledge.kinds')
    }
  }
  @Builder tagFilter() {
    if (this.workspace.tags.length || this.workspace.tag) {
      Button({ type: ButtonType.Normal, buttonStyle: ButtonStyleMode.TEXTUAL }) {
        Row({space:5}) {
          Text(this.workspace.tag ? '#' + this.workspace.tag : tr('knowledge_all_tags'))
            .fontSize(14).maxFontScale(Tokens.interfaceScaleFor(14)).fontColor(Colors.link).flexShrink(1).maxLines(2).textOverflow({overflow:TextOverflow.Ellipsis})
            .id('knowledge.filters.label')
          SymbolGlyph(Symbols.down).fontSize('12vp').fontColor([Colors.link])
        }
      }.constraintSize({minHeight:Math.max(34,Math.ceil(21*Math.min(nativeEnvironment.fontScale,Tokens.interfaceScaleFor(17)))+10),maxWidth:'100%'})
        .padding({left:8,right:8,top:5,bottom:5}).backgroundColor(Color.Transparent)
        .accessibilityText(tr('knowledge_filter_tag')).bindMenu(this.knowledgeFilters).id('knowledge.filters')
    }
  }
  @Builder rootList() {
    Column({space:6}) {
      Row({space:4}) {
        Column() { this.searchField(false) }.layoutWeight(1)
        if (!this.separateTagLine() && !this.workspace.query.trim() && (this.workspace.tags.length || this.workspace.tag))
          Button({type:ButtonType.Circle}) { SymbolGlyph(Symbols.filter).fontSize('16vp').fontColor([Colors.link]) }
            .width(38).height(38).padding(0).backgroundColor(Colors.surface).accessibilityText(tr('knowledge_filter_tag')+' · '+(this.workspace.tag || tr('knowledge_all_tags')))
            .bindMenu(this.knowledgeFilters).id('knowledge.filters')
      }.width('100%').onAreaChange((_:Area,area:Area)=>{this.filterWidth=Number(area.width);})
      if (!this.workspace.query.trim()) {
        this.kindBar()
        if (this.separateTagLine() && (this.workspace.tags.length || this.workspace.tag)) this.tagFilter()
        if (!this.isProjectList()) Row({space:5}) {
          Text(this.workspace.tag ? '#' + this.workspace.tag : tr('all')).fontSize(15).maxFontScale(Tokens.interfaceScaleFor(15)).fontWeight(FontWeight.Medium).fontColor(Colors.secondary)
          Text(String(this.workspace.entries.length)).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).fontColor(Colors.secondary)
        }.width('100%').padding({left:10,top:7,bottom:3})
      }
      Refresh({ refreshing: $$this.refreshing }) { List() {
        this.listStatus()
        if (this.isProjectList()) {
          ForEach(['focus', 'active', 'done', 'ignored'], (section: string) => {
            if (this.projectRows(section).length) {
              ListItem() {
                Row({space:6}) {
                  Text(this.sectionLabel(section)).fontSize(15).maxFontScale(Tokens.interfaceScaleFor(15)).fontWeight(FontWeight.Medium).fontColor(Colors.secondary).id('knowledge.group.title.'+section)
                  Text(String(this.projectRows(section).length)).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).fontColor(Colors.secondary)
                  if (section==='ignored') SymbolGlyph(this.showIgnored ? Symbols.down : Symbols.expand).fontSize('12vp').fontColor([Colors.secondary])
                }.width('100%').padding({left:10,top:16,bottom:8})
                  .accessibilityGroup(true).accessibilityText(this.sectionLabel(section)+' '+String(this.projectRows(section).length))
                  .onClick(() => { if (section==='ignored') this.showIgnored=!this.showIgnored; }).id('knowledge.group.'+section)
              }
              if (section !== 'ignored' || this.showIgnored) ListItemGroup() {
                ForEach(this.projectRows(section), (binding: BoundRow<ResourceRow>) => {
                  ListItem() { this.libraryRow(binding.value) }
                }, (binding: BoundRow<ResourceRow>) => binding.key)
              }.divider({strokeWidth:0.5,color:Colors.divider,startMargin:60}).backgroundColor(Colors.surface).borderRadius(16).clip(true)
            }
          }, (section: string) => section)
        } else if (this.entryRowBindings.length || this.resultRowBindings.length) ListItemGroup() {
          this.sectionEntries()
        }.divider({strokeWidth:0.5,color:Colors.divider,startMargin:56}).backgroundColor(Colors.surface).borderRadius(14).clip(true)

      }.width('100%').height('100%') }.onRefreshing(() => {
        this.workspace.refresh().finally(() => { this.refreshing=false; });
      }).layoutWeight(1).width('100%').id('knowledge.entries')
    }.width('100%').height('100%').padding({left:16,right:16,top:6,bottom:8}).backgroundColor(Colors.grouped)
  }
  @Builder recordingHome() {
    Column({space:6}) {
      List({space:10}) {
        ListItem() {
          Column() {
            Button({type:ButtonType.Normal}) {
              Row({space:10}) {
                SymbolGlyph(Symbols.calendar).fontSize('18vp').fontColor([Colors.link])
                Text(tr('phone_meeting_calendar')).fontSize(16).maxFontScale(Tokens.interfaceScaleFor(16)).fontColor(Colors.link).layoutWeight(1)
                if (this.calendarState==='loading') LoadingProgress().width(18).height(18)
              }.width('100%')
            }.width('100%').constraintSize({minHeight:46}).padding({left:15,right:15,top:8,bottom:8})
              .backgroundColor(Color.Transparent).onClick(() => this.readMeetingCalendar()).id('recordings.calendar')
            if (this.calendarState && !['loading','ready'].includes(this.calendarState))
              Text(tr(this.calendarState)).fontSize(14).maxFontScale(Tokens.interfaceScaleFor(14)).fontColor(Colors.secondary).width('100%').padding({left:15,right:15,bottom:9})
            ForEach(this.calendarEvents,(event:MeetingOccurrence) => {
              Button({type:ButtonType.Normal}) {
                Text(this.clockTime(event.start)+'  '+event.title).fontSize(15).maxFontScale(Tokens.interfaceScaleFor(15)).fontColor(Colors.ink).width('100%')
              }.width('100%').constraintSize({minHeight:44}).padding({left:15,right:15}).backgroundColor(Color.Transparent)
                .onClick(() => { this.meetingKind='meeting'; this.meetingTitle=event.title; this.selectedCalendarEvent=event.id; this.calendarEvents=[]; this.calendarState=''; })
            },(event:MeetingOccurrence) => event.key())
          }.width('100%').borderRadius(16).backgroundColor(Colors.surface)
        }
        ListItem() {
          Column({space:12}) {
            TextInput({text:this.meetingTitle,placeholder:tr(this.meetingKind==='meeting' ? 'phone_record_meeting' : 'phone_record_note')+' · '+new Date().toLocaleDateString()})
              .height(Math.max(42,Math.ceil(24*nativeEnvironment.fontScale)+8)).padding(0).borderRadius(0)
              .backgroundColor(Color.Transparent).onChange((v:string) => { this.meetingTitle=v; this.selectedCalendarEvent=''; }).id('recordings.title')
            Divider().strokeWidth(0.5).color(Colors.divider)
            Row() {
              ForEach(['meeting','note'],(kind:string) => {
                Button({type:ButtonType.Capsule}) {
                  Text(tr(kind==='meeting' ? 'phone_record_meeting' : 'phone_record_note')).fontSize(14).maxFontScale(Tokens.interfaceScaleFor(14))
                    .fontWeight(this.meetingKind===kind ? FontWeight.Bold : FontWeight.Normal).fontColor(Colors.ink)
                }.layoutWeight(1).constraintSize({minHeight:34}).padding({top:3,bottom:3})
                  .backgroundColor(this.meetingKind===kind ? Colors.surface : Color.Transparent)
                  .accessibilitySelected(this.meetingKind===kind).onClick(() => this.meetingKind=kind).id('recordings.kind.'+kind)
              },(kind:string) => kind)
            }.width('100%').borderRadius(18).padding(2).backgroundColor(Colors.control)
            if (this.meetingKind==='meeting' && this.canInvite()) {
              Divider().strokeWidth(0.5).color(Colors.divider)
              Row({space:18}) {
                Column({space:4}) {
                  Text(tr('you').slice(0,1)).width(36).height(36).fontSize('16vp').textAlign(TextAlign.Center)
                    .backgroundColor(Colors.control).borderRadius(18).fontColor(Colors.ink)
                  Text(tr('you')).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).fontColor(Colors.ink)
                }
                Button({type:ButtonType.Normal}) {
                  Column({space:4}) {
                    SymbolGlyph(this.roomRole()==='off' ? Symbols.plus : Symbols.persona).fontSize('18vp').fontColor([Colors.link])
                      .width(36).height(36).border({width:1,radius:18,color:Colors.link})
                    Text(this.roomRole()==='off' ? tr('phone_meeting_invite') : tr('persona')).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).fontColor(Colors.link)
                    if (this.roomRole()!=='off') Text(tr(meetingRoleLabel(this.roomRole()))).fontSize(11).maxFontScale(Tokens.interfaceScaleFor(11)).fontColor(Colors.secondary)
                  }
                }.padding(3).backgroundColor(Color.Transparent).accessibilitySelected(this.roomRole()!=='off')
                  .accessibilityText(tr(this.roomRole()==='off' ? 'phone_meeting_invite' : 'phone_meeting_remove'))
                  .onClick(() => this.invitationOverride=this.roomRole()==='off' ? 'invite' : 'off').id('recordings.invite')
                Blank().layoutWeight(1)
              }.width('100%').alignItems(VerticalAlign.Top)
              if (this.roomRole()!=='off') {
                Text(tr('phone_meeting_live_note')).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).fontColor(Colors.secondary).width('100%').id('recordings.invite.detail')
                Row() {
                  Text(tr('phone_meeting_private')).fontSize(15).maxFontScale(Tokens.interfaceScaleFor(15)).fontColor(Colors.ink).layoutWeight(1)
                  Toggle({type:ToggleType.Switch,isOn:this.privateMeeting}).onChange((value:boolean) => this.privateMeeting=value).id('recordings.private')
                }.width('100%')
              }
            }
            Row({space:9}) {
              SymbolGlyph($r('sys.symbol.mic')).fontSize('17vp').fontColor([Colors.link])
              Text(tr('phone_meeting_local_transcription')).fontSize(15).maxFontScale(Tokens.interfaceScaleFor(15)).fontColor(Colors.ink).layoutWeight(1)
            }.width('100%').constraintSize({minHeight:38}).accessibilityGroup(true).id('recordings.transcription')
          }.width('100%').alignItems(HorizontalAlign.Start).padding(15).borderRadius(16).backgroundColor(Colors.surface)
        }
        ListItem() { Text(tr('phone_record_audio_local')).width('100%').fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).fontColor(Colors.secondary).padding({left:14,right:12,bottom:6}) }
        if (this.visibleLocal().length) ListItem() { PhoneSectionTitle({text:tr('phone_record_local'),trailing:String(this.visibleLocal().length)}) }
        ForEach(this.visibleLocal(), (entry:KnowledgeRecordingEntry) => {
          ListItem() {
            Row({space:10}) {
              SymbolGlyph($r('sys.symbol.mic')).fontSize('19vp').fontColor([Colors.danger])
              Column({space:3}) {
                Text(entry.title || this.localDate(entry.startedAt)).fontSize(16).maxFontScale(Tokens.interfaceScaleFor(16)).fontWeight(FontWeight.Medium).fontColor(Colors.ink).maxLines(2)
                Text(this.clock(entry.duration) + ' · ' + this.localDate(entry.startedAt)).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).fontColor(Colors.secondary)
              }.layoutWeight(1).alignItems(HorizontalAlign.Start)
              SymbolGlyph(Symbols.expand).fontSize('12vp').fontColor([Colors.secondary])
            }.width('100%').padding(13).borderRadius(12).backgroundColor(Colors.surface)
              .onClick(() => this.openRecording(entry.id)).id('recordings.local.' + entry.id)
          }
        }, (entry:KnowledgeRecordingEntry) => JSON.stringify([entry.id,entry.title,entry.duration,entry.uploadState,entry.transcriptionState]))
        if (!this.workspace.query.trim()) ListItem() {
          Row({space:10}) {
            SymbolGlyph(Symbols.calendar).fontSize('19vp').fontColor([Colors.link])
            Text(tr('phone_all_meetings')).fontSize(16).maxFontScale(Tokens.interfaceScaleFor(16)).fontWeight(FontWeight.Medium).fontColor(Colors.ink).layoutWeight(1)
            SymbolGlyph(Symbols.expand).fontSize('12vp').fontColor([Colors.secondary])
          }.width('100%').padding({left:14,right:13,top:6,bottom:6}).constraintSize({minHeight:48}).backgroundColor(Colors.surface).borderRadius(13)
            .onClick(() => this.onAllMeetings()).id('recordings.all')
        }
      }.width('100%').layoutWeight(1).id('recordings.list')
      Row() {
        Button({type:ButtonType.Capsule}) {
          Row({space:8}) {
            SymbolGlyph($r('sys.symbol.mic_fill')).fontSize('18vp').fontColor([Colors.onAction])
            Text(tr(this.recording.captureState === 'recording' ? 'knowledge_recording_active' : this.meetingKind==='meeting' ? 'phone_meeting_start' : 'phone_start_note'))
              .fontSize(16).maxFontScale(Tokens.interfaceScaleFor(16)).fontWeight(FontWeight.Medium).fontColor(Color.White).textAlign(TextAlign.Center)
              .flexShrink(1).id('recordings.capture.label')
          }.width('100%').justifyContent(FlexAlign.Center)
        }.width('100%').constraintSize({minHeight:Math.max(45,Math.ceil(22*Math.min(nativeEnvironment.fontScale,Tokens.interfaceScaleFor(17)))+12)})
          .padding({left:14,right:14,top:6,bottom:6}).backgroundColor(this.recording.canStop() ? Colors.danger : Colors.action).enabled(!this.model.demo && (this.recording.canStart() || this.recording.canStop()))
          .onClick(() => this.openRecording('', !this.recording.canStop())).id('recordings.capture')
      }.width('100%').padding({top:4,bottom:4}).id('knowledge.recording.open')
    }.width('100%').height('100%').padding({left:16,right:16,top:18,bottom:12}).backgroundColor(Colors.grouped).id('recordings.home')
      .bindSheet($$this.recordingOpen,this.recordingSheet(),{title:{title:tr('knowledge_recording_open')},height:SheetSize.LARGE,showClose:true,
        onDisappear:() => { this.recording.stopForBackground().catch(() => undefined); }})
  }
  @Builder listContent() {
    if (this.initialKind === 'recordings' && !this.localMeetings) this.recordingHome()
    else this.rootList()
  }

  private openSafeLink(url: string): void { if (/^https?:\/\//i.test(url.trim())) this.onLink(url.trim()); }

  private askPersona(): void {
    this.workspace.askPersona().then((row: ResourceRow | undefined) => { if (row) this.onChat(row); }).catch(() => undefined);
  }

  private confirmDeleteRecording(): void {
    if (!this.workspace.canDeleteRecording()) return;
    const generation = this.model.connectionGeneration(), scope = this.model.scope(), target = this.workspace.selected.key();
    this.getUIContext().showAlertDialog({ title: tr('knowledge_forget_recording'), message: tr('knowledge_forget_recording_note'),
      primaryButton: { value: tr('cancel'), action: () => {} },
      secondaryButton: { value: tr('delete'), action: () => {
        if (!this.model.owns(generation, scope) || this.workspace.selected.key() !== target) return;
        this.workspace.deleteRecording().catch(() => undefined);
      } } });
  }

  private confirmDispatch(): void {
    const machine = this.workspace.machineForDirectory(this.workspace.targetMachines);
    if (!machine || !this.workspace.agentDraft.trim() || !this.workspace.agentLauncher.trim()) return;
    const generation = this.model.connectionGeneration(), scope = this.model.scope(), target = this.workspace.selected.key();
    this.getUIContext().showAlertDialog({ title: tr('knowledge_start_agent'), message: machine.title + ' · ' + this.workspace.directoryTargets[this.workspace.selectedDirectory]?.path,
      primaryButton: { value: tr('cancel'), action: () => {} },
      secondaryButton: { value: tr('knowledge_start_agent'), action: () => {
        if (!this.model.owns(generation, scope) || this.workspace.selected.key() !== target) return;
        this.workspace.dispatchAgent(machine, this.workspace.agentDraft, this.workspace.agentLauncher)
          .then((row: ResourceRow | undefined) => { if (row) this.onChat(row); }).catch(() => undefined);
      } } });
  }

  private toggleAction(id: string): void {
    this.selectedActions = this.selectedActions.includes(id) ? this.selectedActions.filter((value: string) => value !== id) : this.selectedActions.concat([id]);
  }

  private confirmAddTodos(): void {
    if (!this.selectedActions.length) return;
    const generation = this.model.connectionGeneration(), scope = this.model.scope(), target = this.workspace.selected.key();
    this.getUIContext().showAlertDialog({ title: tr('knowledge_minutes_add_todos'), message: String(this.selectedActions.length),
      primaryButton: { value: tr('cancel'), action: () => {} },
      secondaryButton: { value: tr('knowledge_minutes_add_todos'), action: () => {
        if (!this.model.owns(generation, scope) || this.workspace.selected.key() !== target) return;
        this.workspace.addMinutesTodos(this.selectedActions.slice()).then((saved: boolean) => { if (saved) this.selectedActions = []; }).catch(() => undefined);
      } } });
  }

  private editSaveKey(): string {
    if (this.workspace.writeState === 'unknown') return 'result_unknown';
    return ['sending', 'pending'].includes(this.workspace.writeState) ? this.workspace.writeState : 'save';
  }

  @Builder metadataForm() {
    Scroll() { Column() { this.editForm() }.width('100%').padding(16) }
      .width('100%').height('100%').align(Alignment.TopStart).backgroundColor(Colors.grouped)
  }

  @Builder editForm() {
    Column({ space: Tokens.gap }) {
      Text(tr('knowledge_edit_metadata')).fontSize(Tokens.body).maxFontScale(Tokens.interfaceScaleFor(Tokens.body)).fontWeight(FontWeight.Medium)
      Select(['P0', 'P1', 'P2', 'P3'].map((value: string): SelectOption => ({ value: value }))).attributeModifier(SelectStyles.inline).value(this.workspace.editImportance)
        .onSelect((index: number) => this.workspace.setEditImportance(['P0', 'P1', 'P2', 'P3'][index] || 'P2')).id('knowledge.edit.importance')
      TextInput({ text: this.workspace.editRelevance, placeholder: tr('knowledge_relevance') }).type(InputType.Number)
        .onChange((value: string) => this.workspace.setEditRelevance(value)).id('knowledge.edit.relevance')
      TextInput({ text: this.workspace.editTags, placeholder: tr('knowledge_tags') }).onChange((value: string) => this.workspace.setEditTags(value)).id('knowledge.edit.tags')
      if (this.workspace.selected.path === 'projects') {
        Select([{ value: tr('knowledge_project_short') }, { value: tr('knowledge_project_long') }]).attributeModifier(SelectStyles.inline)
          .value(this.workspace.editProjectKind === 'long' ? tr('knowledge_project_long') : tr('knowledge_project_short') )
          .onSelect((index: number) => this.workspace.setEditProjectKind(index === 1 ? 'long' : 'short')).id('knowledge.edit.project_kind')
        Select([{ value: tr('knowledge_status_active') }, { value: tr('knowledge_status_paused') },
          { value: tr('knowledge_status_done') }, { value: tr('knowledge_status_ignored') }]).attributeModifier(SelectStyles.inline)
          .value(tr('knowledge_status_' + this.workspace.editStatus))
          .onSelect((index: number) => this.workspace.setEditStatus(['active', 'paused', 'done', 'ignored'][index] || 'active')).id('knowledge.edit.status')
      }
      Row({ space: Tokens.gap }) {
        Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('cancel')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('cancel', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('cancel')).bindTips(tr('cancel')).onClick(() => this.workspace.editOpen = false)
        Button({ type: ButtonType.Circle }) { if (IconActions.busy(this.editSaveKey())) LoadingProgress().width(Tokens.symbol).height(Tokens.symbol).color(Colors.onAction); else SymbolGlyph(IconActions.symbol(this.editSaveKey())).fontSize(Tokens.symbolFont).fontColor([IconActions.color(this.editSaveKey(), true)]) }.attributeModifier(ButtonStyles.iconPrimary).accessibilityText(tr(this.editSaveKey())).bindTips(tr(this.editSaveKey())).enabled(this.workspace.canSaveEdit())
          .onClick(() => this.workspace.saveEdit()).id('knowledge.edit.save')
      }.width('100%').justifyContent(FlexAlign.End)
    }.width('100%').alignItems(HorizontalAlign.Start).padding(Tokens.inset)
  }

  private openFacts():void { this.facts.begin(Wire.text(Wire.object(this.workspace.detail,'meta'),'pinned')); }

  @Builder entryEvidence() {
    if (this.workspace.agents.length) {
      Column({ space: Tokens.gap }) {
        Text(tr('knowledge_project_agents')).fontSize(Tokens.body).maxFontScale(Tokens.interfaceScaleFor(Tokens.body)).fontWeight(FontWeight.Medium)
        ForEach(this.agentRowBindings, (binding: BoundRow<ResourceRow>) => Button({ type: ButtonType.Normal }) { this.navigationLabel(binding.value.title || binding.value.id) }.buttonStyle(ButtonStyleMode.TEXTUAL).backgroundColor(Color.Transparent).padding(0).width('100%')
          .onClick(() => this.onChat(binding.value)).id('knowledge.agent.' + binding.value.id), (binding: BoundRow<ResourceRow>) => binding.key)
      }.width('100%').alignItems(HorizontalAlign.Start)
    }
    if (this.workspace.matches.length) {
      Column({ space: Tokens.gap }) {
        Text(tr('knowledge_search_matches')).fontSize(Tokens.body).maxFontScale(Tokens.interfaceScaleFor(Tokens.body)).fontWeight(FontWeight.Medium)
        ForEach(this.workspace.matches, (match: Record<string, Object>, index: number) => Text(Wire.text(match, 'line'))
          .fontSize(Tokens.meta).width('100%').id('knowledge.match.' + String(index)),
          (match: Record<string, Object>, index: number) => JSON.stringify([index, Wire.text(match, 'id'), JSON.stringify(match)]))
      }.width('100%').alignItems(HorizontalAlign.Start)
    }
    if (Wire.strings(this.workspace.detail, 'log').length) {
      Column({ space: Tokens.gap }) { Text(tr('knowledge_timeline')).fontSize(Tokens.body).maxFontScale(Tokens.interfaceScaleFor(Tokens.body)).fontWeight(FontWeight.Medium)
        ForEach(Wire.strings(this.workspace.detail, 'log').slice().reverse(), (line: string, index: number) => Text(line).fontSize(Tokens.meta).width('100%')
          .id('knowledge.timeline.' + String(index)), (line: string, index: number) => JSON.stringify([index, line]))
      }.width('100%').alignItems(HorizontalAlign.Start)
    }
  }

  @Builder recordingDetail() {
    Scroll() {
      Column({space:8}) {
        Row({space:10}) {
          Stack() { SymbolGlyph(this.kindIcon('recordings')).fontSize('20vp').fontColor([Colors.secondary]) }
            .width(36).height(36).backgroundColor(Colors.control).borderRadius(10)
          Text(this.workspace.selected.title || this.workspace.selected.id).fontSize(22).maxFontScale(Tokens.interfaceScaleFor(22)).fontWeight(FontWeight.Bold).fontColor(Colors.ink).layoutWeight(1)
        }.width('100%')
        if (this.workspace.selected.detail) Text(this.workspace.selected.detail).fontSize(16).fontColor(Colors.ink).width('100%')
        Button({type:ButtonType.Capsule}) {
          Row({space:7}) {
            SymbolGlyph(IconActions.symbol('knowledge_ask_persona')).fontSize('18vp').fontColor([Colors.onAction])
            Text(tr('knowledge_ask_persona')).fontSize(17).maxFontScale(Tokens.interfaceFontMaxScale).fontColor(Colors.onAction).textAlign(TextAlign.Center)
              .flexShrink(1).id('knowledge.ask.label')
          }.width('100%').justifyContent(FlexAlign.Center)
        }.width('100%').constraintSize({minHeight:Math.max(46,Math.ceil(23*Math.min(nativeEnvironment.fontScale,Tokens.interfaceScaleFor(17)))+12)})
          .padding({left:14,right:14,top:6,bottom:6}).backgroundColor(Colors.action).onClick(() => this.askPersona()).id('knowledge.ask')
        this.entryEvidence()
        PhoneSectionTitle({text:tr('phone_kb_confirmed')})
        Column({space:6}) {
          if (this.facts.open) {
            if (this.facts.conflict) {
              Text(tr('phone_facts_changed')).fontSize(14).maxFontScale(Tokens.interfaceScaleFor(14)).fontColor(Colors.needs).width('100%')
              Text(this.facts.baseline || tr('empty')).fontSize(15).fontColor(Colors.ink).width('100%').copyOption(CopyOptions.LocalDevice).id('knowledge.facts.latest')
            }
            TextArea({text:this.facts.text,placeholder:tr('phone_kb_confirmed')}).width('100%').height(100)
              .onChange((v:string) => this.facts.edit(v)).id('knowledge.facts.text')
            Row() {
              Button(tr('cancel'),{buttonStyle:ButtonStyleMode.TEXTUAL}).maxFontScale(Tokens.interfaceFontMaxScale).onClick(() => this.facts.close())
              Button(tr('save'),{buttonStyle:ButtonStyleMode.TEXTUAL}).maxFontScale(Tokens.interfaceFontMaxScale)
                .enabled(this.workspace.supportsExpectedPinned && !this.workspace.busy && !['pending','unknown'].includes(this.workspace.writeState) &&
                  this.facts.text.trim()!==Wire.text(Wire.object(this.workspace.detail,'meta'),'pinned').trim())
                .onClick(() => this.facts.save(this.workspace)).id('knowledge.facts.save')
            }
          } else {
            if (Wire.text(Wire.object(this.workspace.detail,'meta'),'pinned')) Text(Wire.text(Wire.object(this.workspace.detail,'meta'),'pinned'))
              .fontSize(16).fontColor(Colors.ink).width('100%')
            if (!this.workspace.loading && !this.workspace.error && Object.keys(this.workspace.detail).length>0 && !this.workspace.supportsExpectedPinned) Text(tr('phone_conditional_read_only'))
              .fontSize(14).maxFontScale(Tokens.interfaceScaleFor(14)).fontColor(Colors.secondary).width('100%').padding({top:8,bottom:8}).id('knowledge.facts.read_only')
            if (this.workspace.supportsExpectedPinned) Button({type:ButtonType.Normal}) {
              Row({space:8}) {
                SymbolGlyph(Wire.text(Wire.object(this.workspace.detail,'meta'),'pinned') ? Symbols.edit : Symbols.plus).fontSize('17vp').fontColor([Colors.link])
                Text(tr(Wire.text(Wire.object(this.workspace.detail,'meta'),'pinned') ? 'edit' : 'phone_kb_add_fact')).fontSize(16).maxFontScale(Tokens.interfaceScaleFor(16)).fontColor(Colors.link)
              }.width('100%')
            }.width('100%').constraintSize({minHeight:44}).padding(0).backgroundColor(Color.Transparent).onClick(() => this.openFacts()).id('knowledge.facts.open')
          }
        }.width('100%').alignItems(HorizontalAlign.Start).padding({left:14,right:14,top:4,bottom:4}).backgroundColor(Colors.surface).borderRadius(14)
        Text(tr('phone_kb_fact_note')).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).fontColor(Colors.secondary).width('100%').padding({left:6,right:6})
        if (Wire.text(this.workspace.detail,'summary')) {
          PhoneSectionTitle({text:tr('phone_kb_profile')})
          Column() { MarkdownText({text:Wire.text(this.workspace.detail,'summary'),onLink:(url:string) => this.openSafeLink(url)}) }
            .width('100%').alignItems(HorizontalAlign.Start).padding(14).backgroundColor(Colors.surface).borderRadius(14).id('knowledge.summary')
        }
        if (Wire.text(this.workspace.detail,'path')) {
          PhoneSectionTitle({text:tr('directory')})
          Text(Wire.text(this.workspace.detail,'path')).fontFamily('monospace').fontSize(13).fontColor(Colors.secondary).width('100%')
            .padding(14).backgroundColor(Colors.surface).borderRadius(14).copyOption(CopyOptions.LocalDevice).id('knowledge.recording.directory')
        }
        if (this.workspace.error && !(this.facts.open && this.facts.conflict && this.workspace.error==='phone_facts_changed')) Text(tr(this.workspace.error)).fontSize(14).maxFontScale(Tokens.interfaceScaleFor(14)).fontColor(Colors.danger).width('100%')
        Button({type:ButtonType.Normal}) {
          Row() {
            Text(tr('knowledge_minutes')+' · '+tr('knowledge_transcript')).fontSize(16).maxFontScale(Tokens.interfaceScaleFor(16)).fontColor(Colors.ink).layoutWeight(1)
            SymbolGlyph(this.recordingTools ? Symbols.down : Symbols.expand).fontSize('14vp').fontColor([Colors.secondary])
          }.width('100%')
        }.width('100%').constraintSize({minHeight:44}).padding(12).backgroundColor(Colors.surface).borderRadius(12)
          .accessibilityDescription(tr(this.recordingTools ? 'control_expanded' : 'control_collapsed')).onClick(() => this.recordingTools=!this.recordingTools).id('knowledge.recording.tools')
        if (this.recordingTools) {
          this.recordingSection(UIUtils.makeBinding(():Record<string,Object> => this.workspace.detail))
          this.relatedEntries(UIUtils.makeBinding(():Record<string,Object> => this.workspace.detail))
          if (this.importedMeeting()) Button(this.importedMeeting()!.title,{buttonStyle:ButtonStyleMode.TEXTUAL}).maxFontScale(Tokens.interfaceFontMaxScale).onClick(() => {
            const item=this.importedMeeting(); if (item) this.onOpenResource(item);
          }).id('knowledge.recording.imported')
          Button(tr('knowledge_edit_metadata'),{buttonStyle:ButtonStyleMode.TEXTUAL}).maxFontScale(Tokens.interfaceFontMaxScale).onClick(() => this.workspace.beginEdit()).id('knowledge.edit.open')
          if (this.workspace.editOpen && !this.metadataSheet) this.editForm()
          Button(tr('knowledge_forget_recording'),{buttonStyle:ButtonStyleMode.TEXTUAL}).maxFontScale(Tokens.interfaceFontMaxScale).fontColor(Colors.danger)
            .enabled(this.workspace.canDeleteRecording()).onClick(() => this.confirmDeleteRecording()).id('knowledge.recording.delete')
        }
      }.width('100%').alignItems(HorizontalAlign.Start).padding({left:16,right:16,top:20,bottom:24})
    }.width('100%').height('100%').align(Alignment.TopStart).backgroundColor(Colors.grouped).id('knowledge.recording.detail')
      .bindSheet($$this.metadataSheet,this.metadataForm(),{title:{title:tr('knowledge_edit_metadata')},height:SheetSize.LARGE,showClose:true,
        onDisappear:() => { this.workspace.editOpen=false; }})
  }

  @Builder detailContent() {
    Scroll() {
      Column({ space: Tokens.section }) {
        Column({ space: Tokens.gap }) {
          if (Wire.text(Wire.object(this.workspace.detail, 'meta'), 'importance')) Text(Wire.text(Wire.object(this.workspace.detail, 'meta'), 'importance') + ' · ' + tr('knowledge_relevance') + ' ' + String(Wire.number(Wire.object(this.workspace.detail, 'meta'), 'relevance')))
            .fontSize(Tokens.meta).fontColor($r('sys.color.font_secondary')).width('100%')
          if (this.workspace.selected.detail) Text(this.workspace.selected.detail).fontSize(Tokens.body).width('100%')
        if (Wire.text(this.workspace.detail, 'summary')) {
          Column({ space: Tokens.gap }) { Text(tr(this.workspace.selected.path === 'recordings' ? 'knowledge_recording_summary' : 'knowledge_profile')).fontSize(Tokens.body).maxFontScale(Tokens.interfaceScaleFor(Tokens.body)).fontWeight(FontWeight.Medium)
            MarkdownText({ text: Wire.text(this.workspace.detail, 'summary'), onLink: (url: string) => this.openSafeLink(url) })
          }.width('100%').alignItems(HorizontalAlign.Start).padding(14).backgroundColor(Colors.surface).borderRadius(14).id('knowledge.summary')
        }

          if (this.workspace.selected.path === 'topics' && Wire.rows(this.workspace.detail,'points').length) {
            ForEach(Wire.rows(this.workspace.detail,'points'), (point:Record<string,Object>) => {
              Column({space:5}) {
                if (Wire.strings(point,'path').length) Text(Wire.strings(point,'path').join(' › ')).fontSize(13).fontColor(Colors.link)
                Text(Wire.text(point,'text')).fontSize(17).fontColor(Colors.ink).copyOption(CopyOptions.LocalDevice)
                if (Wire.text(point,'why')) Text(Wire.text(point,'why')).fontSize(14).fontColor(Colors.secondary)
                if (Wire.text(point,'when')) Text(Wire.text(point,'when')).fontSize(12).fontColor(Colors.secondary)
                ForEach(Wire.rows(point,'sources'), (source:Record<string,Object>) => {
                  if (Wire.text(source,'id') && KB_KINDS.includes(Wire.text(source,'kind'))) {
                    Button(Wire.text(source,'name') || Wire.text(source,'id'),{buttonStyle:ButtonStyleMode.TEXTUAL})
                      .fontSize(14).fontColor(Colors.link).onClick(() => this.openRelated(source))
                  }
                }, (source:Record<string,Object>) => JSON.stringify([Wire.text(source,'kind'),Wire.text(source,'id')]))
              }.width('100%').alignItems(HorizontalAlign.Start).padding(13).backgroundColor(Colors.surface).borderRadius(12)
                .id('knowledge.point.' + Wire.text(point,'id'))
            }, (point:Record<string,Object>) => JSON.stringify(point))
          }
          if (this.workspace.selected.path === 'recordings' && this.importedMeeting()) {
            Button({type:ButtonType.Normal}) {
              Row({space:8}) {
                SymbolGlyph(Symbols.calendar).fontSize('17vp').fontColor([Colors.link])
                Text(this.importedMeeting()!.title).fontSize(16).fontColor(Colors.ink).layoutWeight(1)
                SymbolGlyph(Symbols.expand).fontSize('12vp').fontColor([Colors.secondary])
              }.width('100%').padding(12)
            }.padding(0).width('100%').backgroundColor(Colors.surface).onClick(() => {
              const row = this.importedMeeting(); if (row) this.onOpenResource(row);
            }).id('knowledge.recording.imported')
          }
          Row({ space: Tokens.gap }) {
            Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('knowledge_ask_persona')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('knowledge_ask_persona', true)]) }.attributeModifier(ButtonStyles.iconPrimary).accessibilityText(tr('knowledge_ask_persona')).bindTips(tr('knowledge_ask_persona')).onClick(() => this.askPersona()).id('knowledge.ask')
            Button({ type: ButtonType.Circle }) { SymbolGlyph(Symbols.edit).fontSize(Tokens.symbolFont).fontColor([Colors.link]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('knowledge_edit_metadata'))
              .onClick(() => this.workspace.beginEdit()).id('knowledge.edit.open')
          }.width('100%')
          if (this.workspace.selected.path === 'projects') Button({ type: ButtonType.Normal }) {
            Row({ space: Tokens.gap }) {
            SymbolGlyph(Symbols.agent).fontSize(Tokens.symbolFont).fontColor([Colors.ai])
            Text(tr('knowledge_agent_configuration')).fontSize(Tokens.body).maxFontScale(Tokens.interfaceScaleFor(Tokens.body)).fontColor(Colors.link).layoutWeight(1)
            SymbolGlyph(this.dispatchExpanded ? Symbols.up : Symbols.down).fontSize(Tokens.meta).fontColor([Colors.secondary])
          }.width('100%')
          }.attributeModifier(ButtonStyles.quiet).width('100%').onClick(() => this.dispatchExpanded = !this.dispatchExpanded).accessibilityText(tr('knowledge_agent_configuration')).accessibilityDescription(tr(this.dispatchExpanded ? 'control_expanded' : 'control_collapsed')).id('knowledge.dispatch.toggle')
          if (this.workspace.selected.path === 'projects' && this.dispatchExpanded) {
            TextArea({ text: this.workspace.agentDraft, placeholder: tr('knowledge_agent_prompt') }).height(Tokens.inputExpanded)
              .onChange((value: string) => this.workspace.setAgentDraft(value)).id('knowledge.agent.prompt')
            if (this.workspace.directoryTargets.length) {
              Select(this.directoryOptions()).attributeModifier(SelectStyles.inline).value(this.workspace.directoryTargets[this.workspace.selectedDirectory]?.label || '').onSelect((index: number) => this.workspace.selectedDirectory = index)
                .id('knowledge.agent.directory')
              TextInput({ text: this.workspace.agentLauncher, placeholder: tr('launcher') }).onChange((value: string) => this.workspace.setAgentLauncher(value))
                .id('knowledge.agent.launcher')
              Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('knowledge_start_agent')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('knowledge_start_agent', true)]) }.attributeModifier(ButtonStyles.iconPrimary).accessibilityText(tr('knowledge_start_agent')).bindTips(tr('knowledge_start_agent')).enabled(!!this.workspace.agentDraft.trim() && !!this.workspace.agentLauncher.trim() && !this.workspace.busy).onClick(() => this.confirmDispatch()).id('knowledge.agent.start')
            } else Text(tr('knowledge_agent_unavailable')).fontSize(Tokens.meta).maxFontScale(Tokens.interfaceScaleFor(Tokens.meta)).fontColor($r('sys.color.font_secondary'))
          }
          if (this.workspace.error) Text(tr(this.workspace.error)).fontSize(Tokens.meta).maxFontScale(Tokens.interfaceScaleFor(Tokens.meta)).fontColor($r('app.color.persona_danger')).width('100%')
        }.alignItems(HorizontalAlign.Start).width('100%')
        if (this.workspace.editOpen) this.editForm()
        this.entryEvidence()
        if (this.workspace.selected.path === 'recordings') this.recordingSection(UIUtils.makeBinding((): Record<string, Object> => this.workspace.detail))
        if (this.workspace.selected.path !== 'recordings' && Wire.text(this.workspace.detail, 'recent')) {
          Column({ space: Tokens.gap }) { Text(tr('knowledge_recent')).fontSize(Tokens.body).maxFontScale(Tokens.interfaceScaleFor(Tokens.body)).fontWeight(FontWeight.Medium)
            Text(Wire.text(this.workspace.detail, 'recent')).fontSize(Tokens.meta).width('100%')
          }.width('100%').alignItems(HorizontalAlign.Start)
        }
        if (this.workspace.selected.path === 'projects' && Wire.rows(this.workspace.detail, 'moments').length) {
          Column({ space: Tokens.gap }) {
            Text(tr('knowledge_related_moments')).fontSize(Tokens.body).maxFontScale(Tokens.interfaceScaleFor(Tokens.body)).fontWeight(FontWeight.Medium)
            ForEach(Wire.rows(this.workspace.detail, 'moments'), (moment: Record<string, Object>) => Button({ type: ButtonType.Normal }) { this.navigationLabel(Wire.text(moment, 'author') + ' · ' + Wire.text(moment, 'text')) }.buttonStyle(ButtonStyleMode.TEXTUAL).backgroundColor(Color.Transparent).padding(0).width('100%').onClick(() => this.loadMoment(Wire.text(moment, 'id'))).id('knowledge.moment.' + Wire.text(moment, 'id')),
              (moment: Record<string, Object>, index: number) => JSON.stringify([index, Wire.text(moment, 'id'), JSON.stringify(moment)]))
            if (this.momentText) MarkdownText({ text: this.momentText, onLink: (url: string) => this.openSafeLink(url) })
          }.width('100%').alignItems(HorizontalAlign.Start)
        }
        this.relatedEntries(UIUtils.makeBinding((): Record<string, Object> => this.workspace.detail))
        if (Wire.text(Wire.object(this.workspace.detail, 'meta'), 'url')) Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('knowledge_open_link')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('knowledge_open_link', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('knowledge_open_link')).bindTips(tr('knowledge_open_link')).onClick(() => this.openSafeLink(Wire.text(Wire.object(this.workspace.detail, 'meta'), 'url')))
        if (Wire.text(this.workspace.detail, 'path')) Text(Wire.text(this.workspace.detail, 'path')).fontSize(Tokens.meta).fontColor($r('sys.color.font_secondary')).width('100%')
        if (this.workspace.selected.path === 'recordings') Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('knowledge_forget_recording')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('knowledge_forget_recording', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('knowledge_forget_recording')).bindTips(tr('knowledge_forget_recording')).enabled(this.workspace.canDeleteRecording()).onClick(() => this.confirmDeleteRecording()).id('knowledge.recording.delete')
      }.padding(Tokens.page).width('100%').alignItems(HorizontalAlign.Start)
    }.align(Alignment.TopStart).scrollable(ScrollDirection.Vertical).scrollBar(BarState.Auto).width('100%').height('100%')
  }

  @Builder navigationLabel(label: string) {
    Row({ space: Tokens.gap }) {
      Text(label).fontSize(Tokens.body).fontColor(Colors.ink).layoutWeight(1)
      SymbolGlyph(Symbols.expand).fontSize(Tokens.meta).fontColor([Colors.secondary])
    }.width('100%').constraintSize({ minHeight: Tokens.touch }).padding({ top: Tokens.inset, bottom: Tokens.inset })
  }

  @Builder relatedEntries(detail: Binding<Record<string, Object>>) {
    if (Wire.rows(detail.value, 'links').length) {
      Column({ space: Tokens.gap }) {
        Text(tr('knowledge_related')).fontSize(Tokens.body).maxFontScale(Tokens.interfaceScaleFor(Tokens.body)).fontWeight(FontWeight.Medium)
        ForEach(Wire.rows(detail.value, 'links'), (item: Record<string, Object>) => {
          Button({ type: ButtonType.Normal }) { this.navigationLabel(Wire.text(item, 'title', Wire.text(item, 'id'))) }.buttonStyle(ButtonStyleMode.TEXTUAL).backgroundColor(Color.Transparent).padding(0).width('100%').onClick(() => this.openRelated(item)).id('knowledge.related.' + Wire.text(item, 'kind') + '.' + Wire.text(item, 'id'))
        }, (item: Record<string, Object>, index: number) => JSON.stringify([index, Wire.text(item, 'kind') + '/' + Wire.text(item, 'id'), JSON.stringify(item)]))
      }.width('100%').alignItems(HorizontalAlign.Start)
    }
  }

  @Builder recordingSection(detail: Binding<Record<string, Object>>) {
    Column({ space: Tokens.gap }) {
      if (Object.keys(Wire.object(detail.value, 'minutes')).length) {
        Text(tr('knowledge_minutes')).fontSize(Tokens.body).maxFontScale(Tokens.interfaceScaleFor(Tokens.body)).fontWeight(FontWeight.Medium)
        if (Wire.text(Wire.object(detail.value, 'minutes'), 'markdown')) MarkdownText({ text: Wire.text(Wire.object(detail.value, 'minutes'), 'markdown'), onLink: (url: string) => this.openSafeLink(url) })
        ForEach(Wire.rows(Wire.object(detail.value, 'minutes'), 'actions'), (action: Record<string, Object>) => {
          Row({ space: Tokens.gap }) { Toggle({ type: ToggleType.Checkbox, isOn: this.selectedActions.includes(Wire.text(action, 'id')) }).onChange(() => this.toggleAction(Wire.text(action, 'id')))
            Text(Wire.text(action, 'what')).fontSize(Tokens.meta).layoutWeight(1)
          }.width('100%').id('knowledge.minutes.action.' + Wire.text(action, 'id'))
        }, (action: Record<string, Object>, index: number) => JSON.stringify([index, Wire.text(action, 'id'), JSON.stringify(action)]))
        if (this.selectedActions.length) Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('knowledge_minutes_add_todos')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('knowledge_minutes_add_todos', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('knowledge_minutes_add_todos')).bindTips(tr('knowledge_minutes_add_todos')).onClick(() => this.confirmAddTodos())
      }
      Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('knowledge_minutes_make')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('knowledge_minutes_make', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('knowledge_minutes_make')).bindTips(tr('knowledge_minutes_make')).enabled(!this.workspace.busy && this.workspace.writeState !== 'pending' && this.workspace.writeState !== 'unknown').onClick(() => this.workspace.makeMinutes()).id('knowledge.minutes.make')
      if (Wire.rows(detail.value, 'segments').length) {
        Text(tr('knowledge_transcript')).fontSize(Tokens.body).maxFontScale(Tokens.interfaceScaleFor(Tokens.body)).fontWeight(FontWeight.Medium)
        ForEach(Wire.rows(detail.value, 'segments'), (line: Record<string, Object>, index: number) => Text(Wire.text(line, 'text'))
          .fontSize(Tokens.meta).width('100%').id('knowledge.transcript.' + String(index)),
          (line: Record<string, Object>, index: number) => JSON.stringify([index, JSON.stringify(line)]))
      } else if (Wire.text(detail.value, 'recent')) Text(Wire.text(detail.value, 'recent')).fontSize(Tokens.meta).width('100%')
    }.width('100%').alignItems(HorizontalAlign.Start).id('knowledge.recording.transcript')
  }

  private importedMeeting(): ResourceRow | undefined {
    const selected = this.workspace.selected;
    const merged = Wire.object(selected.data(),'imported_meeting');
    const data = Wire.text(merged,'id') ? merged : Wire.object(Wire.object(this.workspace.detail,'meta'),'imported_meeting');
    if (!Wire.text(data,'id')) return undefined;
    const row = ResourceRow.from(data,'knowledge');
    row.path = 'meetings'; row.group = selected.group; row.manager = selected.manager;
    if (!row.title || row.title === row.id) row.title = tr('knowledge_kind_meetings');
    return row;
  }
  private directoryOptions(): SelectOption[] { return this.workspace.directoryTargets.map((target: KnowledgeDirectory): SelectOption => ({ value: target.label })); }
  private openRelated(item: Record<string, Object>): void { const target = ResourceRow.from(item, 'knowledge'); target.path = Wire.text(item, 'kind'); target.manager = this.workspace.selected.manager; this.onOpenResource(target); }
  private async loadMoment(id: string): Promise<void> { this.momentText = await this.workspace.openMoment(id).catch(() => ''); }

  build() {
    if (this.row && this.workspace.selected.id && this.workspace.selected.path==='recordings') this.recordingDetail();
    else if (this.row && this.workspace.selected.id) this.detailContent();
    else this.listContent();
  }
}
