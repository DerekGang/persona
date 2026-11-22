import { LengthMetrics } from '@kit.ArkUI';
import { i18n } from '@kit.LocalizationKit';
import { Colors } from '../design/Colors';
import { Symbols } from '../design/Symbols';
import { PhoneStyle } from '../design/PhoneStyle';
import { Tokens } from '../design/Tokens';
import { tr, trError } from '../design/Strings';
import { nativeEnvironment } from '../platform/NativeEnvironment';
import { AdminSnapshot, AdminWorkspace } from '../models/AdminWorkspace';
import { AllChatGroup, allAssistantChatGroups, allChatDetail, assistantChatSource, assistantChatSourceKey,
  assistantChatState, assistantChatTitleParts, sidebarConversationTitle, SidebarTaskHistory } from '../models/AssistantRecentChats';
import { ClientModel } from '../models/ClientModel';
import { BoundRow, reconcileBoundRows } from '../models/RowBinding';
import { ResourceRow, Wire } from '../models/Wire';

@ComponentV2
export struct PhoneAllChats {
  @Require @Param model: ClientModel;
  @Require @Param history: SidebarTaskHistory;
  @Event onClose: () => void = () => {};
  @Event onOpen: (row: ResourceRow) => void = () => {};
  @Local filter: string = 'all';
  @Local groups: AllChatGroup[] = [];
  @Local rows: BoundRow<ResourceRow>[] = [];
  @Local options: AdminWorkspace = new AdminWorkspace();
  @Local clock: number = Date.now();
  private timer: number = -1;
  private owner: ClientModel | undefined = undefined;
  private scope: string = '';
  private generation: number = -1;
  private list: Scroller = new Scroller();

  aboutToAppear(): void {
    this.synchronize();
    this.timer = setInterval(() => { this.clock = Date.now(); }, 60000);
  }
  aboutToDisappear(): void { clearInterval(this.timer); this.options.hide(); }
  @Monitor('model', 'model.profile', 'model.connected', 'model.conversations', 'model.features', 'model.mainId',
    'model.mainMachineId', 'history.revision') synchronize(): void {
    const scope = this.model.scope(), generation = this.model.connectionGeneration();
    const rebound = this.owner !== this.model || this.scope !== scope || this.generation !== generation;
    if (rebound) { this.filter = 'all'; this.options.hide(); }
    const previous = this.owner, previousScope = this.scope;
    this.owner = this.model; this.scope = scope; this.generation = generation;
    this.groups = allAssistantChatGroups(this.model.conversations, this.model.mainId, this.model.mainMachineId,
      this.model.profile.id, this.model.profile.manager, this.model.features.includes('conversation_origin'), this.history.known(scope));
    if (!this.groups.some((group: AllChatGroup) => group.id === this.filter)) this.filter = 'all';
    this.bindRows(previous, previousScope);
    if (this.model.connected && !this.model.demo && !this.options.showsConversationOptions(this.model)) {
      this.options.open(this.model, 'conversation_options', true).catch(() => undefined);
    }
    this.history.refresh(this.model, true).catch(() => undefined);
  }
  private bindRows(previous: ClientModel | undefined, previousScope: string): void {
    const selected = this.groups.find((group: AllChatGroup) => group.id === this.filter);
    this.rows = reconcileBoundRows(this.rows, selected?.rows || [], (r: ResourceRow) => r.key(),
      previous, this.model, previousScope, this.scope);
  }
  private choose(key: string): void {
    if (!this.groups.some((g: AllChatGroup) => g.id === key)) return;
    this.filter = key;
    this.bindRows(this.owner, this.scope);
    this.list.scrollEdge(Edge.Top);
  }
  private hasOptions(): boolean {
    return this.model.connected && !this.model.demo && this.options.showsConversationOptions(this.model) &&
      typeof this.options.assistantData['recent_task_chats'] === 'boolean';
  }
  private source(row: ResourceRow): string { return assistantChatSource(row, this.history.known(this.scope)); }
  private detail(row: ResourceRow): string {
    return allChatDetail(row, this.source(row), this.filter,
      this.model.language === 'default' ? i18n.System.getSystemLanguage() : this.model.language, this.clock, tr);
  }
  private spoken(row: ResourceRow): string {
    const src = this.source(row);
    return [sidebarConversationTitle(row,tr), this.filter !== 'all' && src ? tr(assistantChatSourceKey(src,true)) : '',
      this.detail(row)].filter((s:string) => !!s).join(tr('phone_sidebar_label_separator'));
  }
  private sourceIcon(key: string): Resource {
    switch (key) {
      case 'all': return Symbols.spaces;
      case 'mine': return $r('sys.symbol.person');
      case 'inbox': return Symbols.inbox;
      case 'task': return $r('sys.symbol.clock');
      case 'lark_agent': return $r('sys.symbol.paperplane');
      case 'spawned': return Symbols.persona;
      case 'extension': return Symbols.apps;
      default: return Symbols.gear;
    }
  }
  private filterName(key: string): string {
    return tr(key === 'all' ? 'all' : key === 'mine' ? 'phone_all_chats_mine' : assistantChatSourceKey(key));
  }
  @Builder displayMenu(snapshot: AdminSnapshot) {
    Menu() {
      MenuItemGroup({header:tr('phone_sidebar_recent_heading')}) {
        MenuItem({content:tr('phone_sidebar_show_scheduled')}).selectIcon(true).selected(Wire.bool(this.options.assistantData,'recent_task_chats'))
          .enabled(!this.options.saving && !this.model.operationBlocked('/api/v1/assistant'))
          .onClick(() => {
            if (this.hasOptions() && this.options.admits(snapshot,'/api/v1/assistant')) {
              this.options.setRecentTaskChats(snapshot,!Wire.bool(this.options.assistantData,'recent_task_chats')).catch(() => undefined);
            }
          }).id('phone_search.recent_task_chats')
      }
    }
  }
  @Builder heading() {
    Stack({alignContent:Alignment.Center}) {
      Text(tr('flow_all_chats')).fontSize(Tokens.body).maxFontScale(Tokens.navigationTextMaxScale)
        .fontWeight(FontWeight.Medium).fontColor(Colors.ink).maxLines(1)
      Row() {
        Button({type:ButtonType.Circle}) { SymbolGlyph($r('sys.symbol.xmark')).fontSize('18vp').fontColor([Colors.ink]) }
          .width(40).height(40).padding(0).backgroundColor(Colors.surface).border({width:Tokens.divider,color:Colors.divider,radius:20})
          .responseRegion({x:-2,y:-2,width:44,height:44}).accessibilityText(tr('close')).id('all_chats.close').onClick(() => this.onClose())
        Blank().layoutWeight(1)
        if (this.hasOptions()) Button({type:ButtonType.Circle}) {
          Image(Symbols.more).width(19).height(19).fillColor(Colors.link)
        }.width(40).height(40).padding(0).backgroundColor(Colors.surface).border({width:Tokens.divider,color:Colors.divider,radius:20})
          .responseRegion({x:-2,y:-2,width:44,height:44}).accessibilityText(tr('phone_all_chats_display_options'))
          .bindMenu(this.displayMenu(this.options.capture('/api/v1/assistant'))).id('phone_search.display_options')
      }.width('100%').padding({left:Tokens.page,right:Tokens.page})
    }.width('100%').height(54).margin({top:16})
  }
  @Builder filters() {
    Flex({wrap:FlexWrap.Wrap,alignItems:ItemAlign.Center,space:{main:LengthMetrics.vp(6),cross:LengthMetrics.vp(6)}}) {
      ForEach(this.groups,(g: AllChatGroup) => {
        Button({type:ButtonType.Capsule}) {
          Row({space:4}) {
            SymbolGlyph(this.sourceIcon(g.id)).fontSize(Tokens.interfaceFontVp(13,nativeEnvironment.fontScale)).fontColor([this.filter === g.id ? Colors.link : Colors.secondary])
            Text(this.filterName(g.id) + ' ' + String(g.rows.length)).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13))
              .fontWeight(FontWeight.Medium).fontColor(this.filter === g.id ? Colors.link : Colors.secondary).maxLines(1)
          }
        }.height(Math.max(20,Math.round(16*Math.min(nativeEnvironment.fontScale,Tokens.interfaceScaleFor(13)))+4)).padding({left:8,right:8,top:1,bottom:1}).backgroundColor(this.filter === g.id ? Colors.selection : Colors.control)
          .accessibilityText(tr('phone_all_chats_filter_count').replace('{name}',this.filterName(g.id)).replace('{count}',String(g.rows.length)))
          .accessibilitySelected(this.filter === g.id).onClick(() => this.choose(g.id)).id('all_chats.filter.'+g.id)
      },(g: AllChatGroup) => g.id+':'+String(g.rows.length))
    }.width('100%').padding({left:Tokens.page,right:Tokens.page,top:19,bottom:19}).id('all_chats.filters')
  }
  @Builder chat(binding: BoundRow<ResourceRow>) {
    Row({space:Tokens.inset}) {
      Column({space:3}) {
        Row({space:4}) {
          Text() {
            Span(assistantChatTitleParts(sidebarConversationTitle(binding.value,tr))[0]).fontColor(Colors.secondary)
            Span(assistantChatTitleParts(sidebarConversationTitle(binding.value,tr))[1]).fontColor(Colors.ink)
          }.fontSize(Tokens.body).maxFontScale(Tokens.interfaceScaleFor(Tokens.body)).maxLines(1)
            .textOverflow({overflow:TextOverflow.Ellipsis}).flexShrink(1)
          if (this.source(binding.value) === 'lark_agent') Image($r('app.media.brand_lark'))
            .width(PhoneStyle.formSize(16,21,nativeEnvironment.fontScale)).aspectRatio(1).objectFit(ImageFit.Contain)
            .accessibilityLevel('no')
          else if (this.source(binding.value)) SymbolGlyph(this.sourceIcon(this.source(binding.value)))
            .fontSize(Tokens.interfaceFontVp(13,nativeEnvironment.fontScale)).fontColor([Colors.secondary])
        }.width('100%').alignItems(VerticalAlign.Center)
        if (this.detail(binding.value)) Text(this.detail(binding.value)).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13))
          .fontColor(Colors.secondary).maxLines(1).textOverflow({overflow:TextOverflow.Ellipsis})
      }.layoutWeight(1).alignItems(HorizontalAlign.Start)
      if (assistantChatState(binding.value) === 'working') LoadingProgress().width(13).height(13).color(Colors.working)
      else if (['needs_you','unread'].includes(assistantChatState(binding.value))) Circle().width(8).height(8)
        .fill(assistantChatState(binding.value) === 'needs_you' ? Colors.needs : Colors.link)
    }.width('100%').constraintSize({minHeight:PhoneStyle.formSize(74,89,nativeEnvironment.fontScale)}).padding({left:20,right:16,top:8,bottom:8}).alignItems(VerticalAlign.Center)
      .accessibilityGroup(true).accessibilityText(this.spoken(binding.value)).onClick(() => {
        const row=binding.value;
        if (this.model.owns(this.generation,this.scope) && this.model.conversations.includes(row)) this.onOpen(row);
      }).id('all_chats.row.'+(binding.value.session || binding.value.id))
  }
  build() {
    Column() {
      this.heading()
      List({scroller:this.list}) {
        if (this.hasOptions() && this.options.error) ListItem() {
          Text(trError(this.options.error)).fontSize(Tokens.meta).fontColor(Colors.danger).width('100%')
            .padding({left:Tokens.page,right:Tokens.page}).id('phone_search.options_error')
        }
        ListItem() { this.filters() }
        ForEach(this.rows,(binding:BoundRow<ResourceRow>) => {
          ListItem() {
            Column() { this.chat(binding); Divider().strokeWidth(Tokens.divider).color(Colors.divider).margin({left:20,right:16}) }.width('100%')
          }
        },(binding:BoundRow<ResourceRow>) => binding.key)
        if (!this.rows.length) ListItem() {
          Column({space:Tokens.gap}) {
            Text(tr('phone_all_chats_empty')).fontSize(Tokens.body).fontWeight(FontWeight.Medium).fontColor(Colors.ink)
            Text(tr('phone_all_chats_empty_detail')).fontSize(Tokens.meta).fontColor(Colors.secondary).textAlign(TextAlign.Center)
          }.width('100%').padding(Tokens.section)
        }
      }.width('100%').layoutWeight(1)
    }.width('100%').height('100%').backgroundColor(Colors.canvas).id('shell.all_chats')
  }
}
