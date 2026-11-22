import { Colors } from '../design/Colors';
import { Symbols } from '../design/Symbols';
import { Tokens } from '../design/Tokens';
import { tr } from '../design/Strings';
import { ClientModel } from '../models/ClientModel';
import { ResourceRow, Wire } from '../models/Wire';
import { nativeEnvironment } from '../platform/NativeEnvironment';

const STAGES:string[]=['plan','develop','review','merge','deploy','verify'];

@ComponentV2
export struct AssistantBriefCard {
  @Require @Param model:ClientModel;
  @Require @Param item:Record<string,Object>;
  @Param active:boolean=false;
  @Event onOpen:(row:ResourceRow)=>void=()=>{};
  @Local pastOpen:boolean=false;
  @Computed get body():Record<string,Object> { return Wire.object(this.item,'body'); }

  private owned():boolean {
    const conversation=Wire.text(this.item,'conversation'), manager=Wire.text(this.item,'manager');
    return this.active && this.model.connected && this.model.selected.kind==='conversation' &&
      (!conversation || conversation===this.model.selected.id) && (!manager || manager===this.model.profile.manager);
  }

  private destination(section:string,row:Record<string,Object>):ResourceRow|undefined {
    if (!this.owned()) return undefined;
    const machine=Wire.text(row,'machine_id'), session=Wire.text(row,'session_id');
    if (machine && session) return this.model.sessions.find((agent:ResourceRow) => agent.machine===machine && (agent.session || agent.id)===session &&
      agent.manager===this.model.profile.manager && (!agent.group || agent.group===this.model.profile.id));
    if (!['accept','progress','done','failed'].includes(section) || section==='failed' && Wire.text(row,'kind')!=='task') return undefined;
    const id=Wire.text(row,'id');
    if (!id) return undefined;
    const todo=new ResourceRow(id,Wire.text(row,'title'),'todo');
    todo.group=this.model.profile.id; todo.manager=this.model.profile.manager;
    return todo;
  }

  private count(section:string):number {
    return Math.max(Wire.rows(this.body,section).length,Wire.number(this.body,section+'_total'));
  }

  @Builder stages(stage:string) {
    Column({space:3}) {
      Row() {
        ForEach(STAGES,(name:string,index:number) => {
          Column({space:2}) {
            Stack({alignContent:Alignment.Center}) {
              Row() {
                Divider().strokeWidth(1).color(index===0 ? Color.Transparent : Colors.divider).layoutWeight(1)
                Divider().strokeWidth(1).color(index===STAGES.length-1 ? Color.Transparent : Colors.divider).layoutWeight(1)
              }.width('100%')
              Stack() {
                Circle().width(12).height(12).fill(index<=STAGES.indexOf(stage) ? name===stage ? Colors.link : Colors.secondary : Colors.surface)
                  .stroke(index<=STAGES.indexOf(stage) ? Color.Transparent : Colors.divider).strokeWidth(1)
                if (index<STAGES.indexOf(stage)) SymbolGlyph(Symbols.check).fontSize('8vp').fontColor([Colors.onAction])
              }
            }.width('100%')
            if (nativeEnvironment.fontScale<1.5) Text(tr('session_brief_stage_'+name)).fontSize(10)
              .fontColor(name===stage ? Colors.link : Colors.secondary).fontWeight(name===stage ? FontWeight.Medium : FontWeight.Normal)
              .textAlign(TextAlign.Center).maxLines(1)
          }.layoutWeight(1)
        },(name:string) => name)
      }.width('100%')
      if (nativeEnvironment.fontScale>=1.5) Text(tr('session_brief_stage_'+stage)).fontSize(13).fontColor(Colors.link)
    }.width('100%').accessibilityGroup(true).accessibilityText(tr('session_brief_stage_'+stage))
      .id('assistant.brief.stage.'+stage)
  }

  @Builder section(section:string,key:string) {
    Column({space:4}) {
      Text(tr(key)+' ('+this.count(section)+')').fontSize(13).fontWeight(FontWeight.Medium).fontColor(Colors.secondary)
      ForEach(Wire.rows(this.body,section),(row:Record<string,Object>) => {
        Column({space:3}) {
          Row({space:8}) {
            Text(Wire.text(row,'title') || Wire.text(row,'why')).fontSize(15).fontWeight(FontWeight.Medium)
              .fontColor(Colors.ink).wordBreak(WordBreak.BREAK_WORD).layoutWeight(1)
            if (this.destination(section,row)) Button(tr('open'),{type:ButtonType.Capsule}).height(30)
              .responseRegion({x:0,y:-7,width:'100%',height:44}).fontSize(12).maxFontScale(Tokens.navigationTextMaxScale)
              .padding({left:12,right:12}).backgroundColor(Colors.control).fontColor(Colors.link)
              .onClick(() => { const to=this.destination(section,row); if (to) this.onOpen(to); })
              .id('assistant.brief.'+section+'.'+Wire.text(row,'id'))
          }.width('100%')
          if (Wire.text(row,'detail') || Wire.text(row,'why')) Text(Wire.text(row,'detail') || Wire.text(row,'why')).fontSize(13).fontColor(Colors.secondary)
          if (Wire.text(row,'from')) Text(Wire.text(row,'from')).fontSize(12).fontColor(Colors.secondary)
          if (section==='progress' && STAGES.includes(Wire.text(row,'stage'))) this.stages(Wire.text(row,'stage'))
          else if (section==='progress' && Wire.text(row,'stage')) Text(Wire.text(row,'stage')).fontSize(12).fontColor(Colors.secondary)
        }.width('100%').alignItems(HorizontalAlign.Start).padding({top:4,bottom:4})
      },(row:Record<string,Object>,index:number) => JSON.stringify([section,Wire.text(row,'id'),index]))
      if (this.count(section)>Wire.rows(this.body,section).length) Text(tr('session_task_more').replace('{count}',String(this.count(section)-Wire.rows(this.body,section).length))).fontSize(12).fontColor(Colors.secondary)
    }.width('100%').alignItems(HorizontalAlign.Start).id('assistant.brief.'+section)
  }

  build() {
    Column({space:8}) {
      Text(Wire.text(this.body,'title') || tr('assistant_detail_brief')).fontSize(14).fontWeight(FontWeight.Medium).fontColor(Colors.secondary)
      if (this.count('waiting')) this.section('waiting','needs_you')
      Row({space:5}) {
        Text(tr('session_brief_past').replace('{hours}',String(Wire.number(this.body,'hours')))
          .replace('{done}',String(this.count('done'))).replace('{failed}',String(this.count('failed'))))
          .fontSize(13).fontWeight(FontWeight.Medium).fontColor(Colors.secondary).layoutWeight(1)
        SymbolGlyph(this.pastOpen ? Symbols.down : Symbols.expand).fontSize('12vp').fontColor([Colors.secondary])
      }.width('100%').constraintSize({minHeight:36}).onClick(() => this.pastOpen=!this.pastOpen).id('assistant.brief.past')
      if (this.pastOpen) {
        if (this.count('failed')) this.section('failed','assistant_feed_state_failed')
        if (this.count('done')) this.section('done','done')
      }
      if (this.count('accept')) { Divider().color(Colors.divider); this.section('accept','needs_you') }
      if (this.count('later')) { Divider().color(Colors.divider); this.section('later','interaction_question_later') }
      if (this.count('progress')) { Divider().color(Colors.divider); this.section('progress','todo_state_working') }
    }.width('100%').alignItems(HorizontalAlign.Start).padding(14).backgroundColor(Colors.surface)
      .border({width:Tokens.divider,color:Colors.divider,radius:Tokens.card}).borderRadius(Tokens.card).id('assistant.brief.card')
  }
}
