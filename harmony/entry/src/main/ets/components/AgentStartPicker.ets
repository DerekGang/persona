import { AgentLaunchChoices, LaunchChoice } from '../models/AgentLaunchChoices';
import { Colors } from '../design/Colors';
import { Symbols } from '../design/Symbols';
import { PhoneSectionTitle } from '../design/PhoneStyle';
import { tr, trError } from '../design/Strings';

@ComponentV2
export struct AgentStartPicker {
  @Require @Param kind:string;
  @Require @Param choices:AgentLaunchChoices;
  @Param options: LaunchChoice[] = [];
  @Param selected:string = '';
  @Param heading:string = '';
  @Param manual:boolean = false;
  @Local query:string = '';
  @Local address:string = '';
  @Event back:() => void = () => {};
  @Event choose:(id:string) => void = () => {};
  @Event browse:(path:string) => void = () => {};
  aboutToAppear():void { this.address = this.choices.path; }
  @Monitor('choices.path') resolved():void { if (this.choices.path) this.address = this.choices.path; }
  @Computed get filtered():LaunchChoice[] {
    const term = this.query.trim().toLowerCase();
    return this.options.filter((o:LaunchChoice) => !term || (o.title+' '+o.detail+' '+o.id).toLowerCase().includes(term));
  }
  @Builder item(o:LaunchChoice,folder:boolean) {
    Row({space:11}) {
      SymbolGlyph(folder || this.kind === 'workspace' ? Symbols.folder : this.kind === 'launcher' ? Symbols.agent : Symbols.apps)
        .fontSize(18).fontColor([Colors.link])
      Column({space:3}) {
        Text(o.title).fontSize(16).fontWeight(FontWeight.Medium).fontColor(Colors.ink).maxLines(2)
        if (o.detail) Text(o.detail).fontSize(13).fontColor(Colors.secondary).maxLines(2).textOverflow({overflow:TextOverflow.Ellipsis})
      }.layoutWeight(1).alignItems(HorizontalAlign.Start)
      if (folder || o.id === this.selected) SymbolGlyph(folder ? Symbols.expand : Symbols.check).fontSize(15).fontColor([folder ? Colors.secondary : Colors.link])
    }.width('100%').constraintSize({minHeight:47}).padding({left:14,right:13,top:7,bottom:7})
      .backgroundColor(Colors.surface).borderRadius(10).enabled(!o.disabled).opacity(o.disabled ? 0.65 : 1).onClick(() => { if (folder) this.browse(o.id); else this.choose(o.id); })
      .accessibilityGroup(true).id(folder ? 'agent.folder.item' : 'agent.option')
  }
  build() {
    Column() {
      Row({space:9}) {
        Button({type:ButtonType.Circle}) { SymbolGlyph(Symbols.collapse).fontSize(18).fontColor([Colors.link]) }
          .width(36).height(36).padding(0).backgroundColor(Colors.surface).accessibilityText(tr('back')).onClick(() => this.back()).id('agent.picker.back')
        Text(this.heading).layoutWeight(1).textAlign(TextAlign.Center).fontSize(18).fontWeight(FontWeight.Bold).fontColor(Colors.ink)
        Blank().width(36)
      }.width('100%').padding({left:14,right:14,top:7,bottom:8})
      if (this.kind === 'folder') {
        Row({space:6}) {
          TextInput({text:this.address,placeholder:tr('flow_folder_search')}).layoutWeight(1).height(41).fontSize(15)
            .onChange((v:string) => this.address = v).onSubmit(() => this.browse(this.address)).id('agent.folder.address')
          Button(tr('open'),{buttonStyle:ButtonStyleMode.TEXTUAL}).fontSize(15).onClick(() => this.browse(this.address)).id('agent.folder.go')
        }.width('100%').padding({left:14,right:14})
      } else if (this.kind === 'workspace' || this.options.length > 5 || this.manual) {
        Search({value:this.query,placeholder:tr(this.kind === 'workspace' ? 'flow_workspace_search' : 'search')}).height(40)
          .onChange((v:string) => this.query = v).margin({left:14,right:14,bottom:8}).id('agent.picker.search')
      }
      List({space:3}) {
        if (this.choices.loading && (this.kind === 'folder' || this.kind === 'model'))
          ListItem() { LoadingProgress().width(25).height(25).margin(12) }
        if (this.choices.error && (this.kind === 'folder' || this.kind === 'model')) ListItem() {
          Text(trError(this.choices.error)).fontSize(14).fontColor(Colors.secondary).padding(12)
        }
        if (this.kind === 'folder') {
          if (this.choices.parent && this.choices.parent !== this.choices.path) ListItem() {
            Button(tr('session_tools_parent_folder'),{buttonStyle:ButtonStyleMode.TEXTUAL}).fontSize(15).onClick(() => this.browse(this.choices.parent)).id('agent.folder.up')
          }
          ForEach(this.choices.directories,(o:LaunchChoice) => { ListItem() { this.item(o,true) } },(o:LaunchChoice) => o.id)
          if (this.choices.path && !this.choices.directories.length) ListItem() { Text(tr('flow_folder_empty')).fontSize(14).fontColor(Colors.secondary).padding(14) }
          if (this.choices.recentDirectories.length) ListItem() { PhoneSectionTitle({text:tr('recent')}) }
          ForEach(this.choices.recentDirectories.slice(0,6),(o:LaunchChoice) => { ListItem() { this.item(o,true) } },(o:LaunchChoice) => o.id)
        } else {
          if (this.choices.locked && this.kind === 'model') ListItem() { Text(this.choices.locked).fontSize(14).fontColor(Colors.secondary).padding(12) }
          ForEach(this.filtered,(o:LaunchChoice) => { ListItem() { this.item(o,false) } },(o:LaunchChoice) => JSON.stringify([o.id,o.title,o.detail,o.disabled]))
          if (this.manual && this.query.trim() && !this.options.some((o:LaunchChoice) => o.id === this.query.trim()) && !this.choices.locked) ListItem() {
            this.item(new LaunchChoice(this.query.trim(),this.query.trim()),false)
          }
          if (!this.filtered.length && !this.manual && !this.choices.loading) ListItem() { Text(tr('no_results')).fontSize(14).fontColor(Colors.secondary).padding(12) }
        }
      }.width('100%').layoutWeight(1).padding({left:14,right:14,top:7,bottom:10})
      if (this.kind === 'folder') Button(tr('flow_use_folder')).fontSize(16).height(47).width('92%').margin({top:5,bottom:12})
        .enabled(!this.choices.loading && !!this.choices.path && !this.choices.error).onClick(() => this.choose(this.choices.path)).id('agent.folder.use')
    }.width('100%').height('100%').backgroundColor(Colors.grouped).id('agent.picker')
  }
}
