import { i18n } from '@kit.LocalizationKit';
import { Colors } from '../design/Colors';
import { IconActions } from '../design/IconActions';
import { PhoneIconButton } from '../design/PhoneStyle';
import { tr, trError } from '../design/Strings';
import { Symbols } from '../design/Symbols';
import { Tokens } from '../design/Tokens';
import { AgentHeaderWords, AgentSessionInfo, agentHeaderWords, sessionInfoPath } from '../models/AgentSessionInfo';
import { ClientModel } from '../models/ClientModel';
import { SessionSpace } from '../models/SessionSpace';
import { Wire } from '../models/Wire';
import { nativeEnvironment } from '../platform/NativeEnvironment';

@ComponentV2
export struct PhoneSessionHeading {
  @Require @Param model: ClientModel;
  @Require @Param space: SessionSpace;
  @Require @Param heading: string;
  @Local info: AgentSessionInfo = new AgentSessionInfo();
  @Local available: number = 0;
  aboutToDisappear(): void { this.info.close(); }
  @Monitor('model','model.profile','model.selected','model.connected') retarget(): void {
    if (this.info.shown && !this.info.owns(this.model)) this.info.close();
  }
  @Computed get words(): AgentHeaderWords {
    const row = this.model.selected;
    return agentHeaderWords(row,this.model.machineName(row.machine),this.space.title || Wire.text(row.data(),'space_name'),this.space.tabs.length > 0,tr);
  }
  private mark(): string {
    const data = this.model.selected.data();
    return Wire.text(data,'agent',Wire.text(data,'launcher')).toLowerCase();
  }
  private tone(): ResourceColor {
    const state = this.words.state;
    return state === 'needs_you' ? Colors.needs : state === 'working' ? Colors.working : state === 'unread' ? Colors.unread : Colors.secondary;
  }
  @Computed get visibleSubtitle(): string {
    if (!this.model.connected) return tr('offline');
    if (this.words.short === this.words.full || !this.available) return this.words.full;
    const ui = this.getUIContext(), scale = nativeEnvironment.fontScale;
    const width = ui.px2vp(ui.getMeasureUtils().measureText({textContent:this.words.full,fontSize:Tokens.interfaceFontVp(12,scale)}));
    // A quiet compact title keeps the place first. An expanded or accessibility-size bar can also name its machine.
    const compactQuiet = ['idle','offline','unknown'].includes(this.words.state) && this.available <= 270 && scale <= 1.1;
    return compactQuiet || width + 30 > this.available ? this.words.short : this.words.full;
  }
  private markSize(): number { return 12*Math.min(nativeEnvironment.fontScale,Tokens.navigationTextMaxScale); }
  private raw(key: string): string { return Wire.text(this.info.detail,key,Wire.text(this.model.selected.data(),key)); }
  private folder(): string {
    const host = this.model.machines.find((machine) => machine.id === this.model.selected.machine);
    return sessionInfoPath(this.raw('cwd'),host ? Wire.text(host.data(),'home') : '');
  }
  private effort(): string {
    const value = this.raw('effort');
    return value === 'xhigh' ? tr('phone_session_extra_high') : ['low','medium','high','max'].includes(value) ? tr(value) : value;
  }
  private permission(): string {
    const value = this.raw('permission_mode');
    return ['default','plan','auto'].includes(value) ? tr('assistant_feed_permission_'+value) : value;
  }
  private tokenCount(key: string): string {
    const n = this.info.detail[key];
    if (typeof n !== 'number' || !Number.isFinite(n)) return '';
    const text = n >= 1000000 ? String(Math.round(n/100000)/10)+'M' : n >= 1000 ? String(Math.round(n/100)/10)+'K' : String(n);
    return text + ' tokens';
  }
  private started(): string {
    const date = new Date(this.raw('started_at'));
    if (!Number.isFinite(date.getTime())) return '';
    const locale = this.model.language === 'default' ? i18n.System.getSystemLanguage() : this.model.language;
    return date.toLocaleString(locale,{year:'numeric',month:'short',day:'numeric',hour:'numeric',minute:'2-digit'});
  }
  @Builder emblem() {
    if (this.mark().includes('claude')) Image($r('app.media.project_agent_claude')).width(this.markSize()).height(this.markSize()).objectFit(ImageFit.Contain)
    else if (this.mark().includes('codex') || this.mark()==='openai') Image($r('app.media.project_agent_openai')).width(this.markSize()).height(this.markSize()).objectFit(ImageFit.Contain)
    else SymbolGlyph(Symbols.agent).fontSize('12vp').fontColor([Colors.secondary])
  }
  @Builder entry(label: string, value: string, id: string) {
    if (value) Row({space:9}) {
      Text(label).fontSize(15).maxFontScale(Tokens.interfaceScaleFor(15)).fontColor(Colors.ink).layoutWeight(1)
      Text(value).fontSize(15).maxFontScale(Tokens.interfaceScaleFor(15)).fontColor(Colors.secondary).textAlign(TextAlign.End)
        .constraintSize({maxWidth:'68%'}).maxLines(2).textOverflow({overflow:TextOverflow.Ellipsis})
    }.width('100%').constraintSize({minHeight:44}).padding({left:14,right:14,top:5,bottom:5}).accessibilityGroup(true).id('session.info.'+id)
  }
  @Builder sectionTitle(key: string) {
    Text(tr(key)).width('100%').fontSize(14).maxFontScale(Tokens.interfaceScaleFor(14)).fontColor(Colors.secondary).padding({left:6,top:17,bottom:6})
  }
  @Builder contents() {
    Column() {
      Row() {
        PhoneIconButton({symbol:IconActions.symbol('close'),label:tr('close'),identity:'session.info.close',action:() => this.info.close()})
        Text(tr('phone_session_info')).layoutWeight(1).textAlign(TextAlign.Center).fontSize(17).fontWeight(FontWeight.Bold).fontColor(Colors.ink)
        Blank().width(44)
      }.width('100%').padding({left:14,right:14,top:6,bottom:5})
      Scroll() {
        Column() {
          Text(this.heading).width('100%').fontSize(22).fontWeight(FontWeight.Bold).fontColor(Colors.ink).maxLines(3).id('session.info.title')
          Text(this.words.status).width('100%').fontSize(15).fontColor(this.tone()).margin({top:3})
          if (this.words.location) Text(this.words.location).width('100%').fontSize(14).fontColor(Colors.secondary).margin({top:4}).id('session.info.place')
          if (this.info.loading) LoadingProgress().width(24).height(24).margin({top:14})
          if (this.info.error) Text(trError(this.info.error)).fontSize(14).fontColor(Colors.secondary).width('100%').margin({top:12})
          if (Object.keys(this.info.detail).length) {
            this.sectionTitle('phone_session_runtime')
            Column() {
              this.entry(tr('model'),this.raw('model').split('/').pop() || '', 'model')
              this.entry(tr('launcher'),this.raw('launcher'),'launcher')
              this.entry(this.mark().includes('claude') ? 'Claude Code' : this.mark().includes('codex') ? 'Codex' : tr('version'),this.raw('version'),'version')
              this.entry(tr('permissions'),this.permission(),'permissions')
              this.entry(tr('phone_session_effort'),this.effort(),'effort')
            }.width('100%').backgroundColor(Colors.surface).borderRadius(15)
            if (!Wire.bool(this.info.detail,'usage_unknown')) {
              this.sectionTitle('phone_session_usage')
              Column() {
                this.entry(tr('phone_session_context'),this.tokenCount('context_tokens'),'context')
                this.entry(tr('chat'),typeof this.info.detail['turns'] === 'number' ? tr(this.info.detail['turns']===1 ? 'phone_session_turn_one' : 'phone_session_turns').replace('{n}',String(this.info.detail['turns'])) : '','turns')
                this.entry(tr('phone_session_output'),this.tokenCount('output_tokens'),'output')
                this.entry(tr('phone_session_started'),this.started(),'started')
              }.width('100%').backgroundColor(Colors.surface).borderRadius(15)
            }
            this.sectionTitle('location')
            Column() {
              this.entry(tr('phone_session_folder'),this.folder(),'folder')
              this.entry(tr('branch'),this.raw('git_branch'),'branch')
            }.width('100%').backgroundColor(Colors.surface).borderRadius(15)
          }
        }.width('100%').padding({left:16,right:16,top:12,bottom:32}).alignItems(HorizontalAlign.Start)
      }.width('100%').layoutWeight(1)
    }.width('100%').height('100%').backgroundColor(Colors.grouped).id('session.info')
  }
  build() {
    Column({space:2}) {
      Text(this.heading).fontSize(17).maxFontScale(Tokens.navigationTextMaxScale).fontWeight(600).fontColor(Colors.ink)
        .maxLines(1).textOverflow({overflow:TextOverflow.Ellipsis})
      Row({space:4}) {
        this.emblem()
        Circle().width(7).height(7).fill(this.tone())
        Text() {
          if (this.model.connected && ['needs_you','working','unread'].includes(this.words.state)) {
            Span(this.words.status).fontColor(this.tone()).fontWeight(600)
            Span(this.visibleSubtitle.slice(this.words.status.length)).fontColor(Colors.secondary)
          } else Span(this.visibleSubtitle).fontColor(Colors.secondary)
        }.fontSize(12).maxFontScale(Tokens.interfaceScaleFor(12)).fontColor(Colors.secondary)
          .maxLines(1).textOverflow({overflow:TextOverflow.Ellipsis}).flexShrink(1).id('chat.title.subtitle')
      }.justifyContent(FlexAlign.Center).constraintSize({maxWidth:'100%'})
    }.width('100%').alignItems(HorizontalAlign.Center).justifyContent(FlexAlign.Center).constraintSize({minHeight:44})
      .onAreaChange((_:Area,area:Area) => { this.available = Number(area.width); })
      .onClick(() => { this.info.open(this.model).catch(() => undefined); }).accessibilityGroup(true).accessibilityRole(AccessibilityRoleType.BUTTON)
      .accessibilityText(tr('phone_session_info_action').replace('{title}',this.heading)).id('chat.title')
      .bindSheet(this.info.shown,this.contents(),{height:'56%',detents:['56%',SheetSize.LARGE],dragBar:true,showClose:false,
        backgroundColor:Colors.grouped,onWillDismiss:(action:DismissSheetAction) => { this.info.close(); action.dismiss(); }})
  }
}
