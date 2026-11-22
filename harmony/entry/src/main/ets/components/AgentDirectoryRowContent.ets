import { LengthMetrics } from '@kit.ArkUI';
import { ProjectAgent } from '../models/ProjectCatalog';
import { Colors } from '../design/Colors';
import { Symbols } from '../design/Symbols';
import { Tokens } from '../design/Tokens';
import { tr } from '../design/Strings';
import { ProjectAgentPlaceLine } from './ProjectAgentPlaceLine';

@ComponentV2
export struct AgentDirectoryRowContent {
  @Require @Param agent: ProjectAgent;
  @Param age: string = '';
  @Param place: string = '';
  @Param showsSource: boolean = false;
  @Param sourceTitle: string = '';
  @Param starter: boolean = false;
  @Param narrow: boolean = false;
  @Param leftInset: number = 20;
  @Param controlScale: number = 1;
  @Param fontScale: number = 1;
  @Param tint: ResourceColor = Colors.secondary;
  @Param activityList: boolean = false;

  @Builder agentMark(agent:ProjectAgent) {
    Stack({ alignContent: Alignment.BottomEnd }) {
      Stack() {
        if (agent.runtime.toLowerCase().includes('claude')) Image($r('app.media.project_agent_claude')).width(17*this.controlScale).height(17*this.controlScale).objectFit(ImageFit.Contain)
        else if (agent.runtime.toLowerCase().includes('codex') || agent.runtime.toLowerCase().includes('openai'))
          Image($r('app.media.project_agent_openai')).width(17*this.controlScale).height(17*this.controlScale).fillColor(Colors.ink).objectFit(ImageFit.Contain)
        else if (agent.runtime) Text(agent.runtime.slice(0,1).toUpperCase()).fontSize('16vp').fontWeight(FontWeight.Medium).fontColor(Colors.ink)
        else SymbolGlyph(Symbols.agent).fontSize('17vp').fontColor([Colors.secondary])
      }.width(36*this.controlScale).height(36*this.controlScale).backgroundColor(Colors.subtle).borderRadius(18*this.controlScale)
      if (this.activityList && agent.state==='working' && agent.rank!==4) Stack() {
        Circle().width(17).height(17).fill(Color.Transparent).stroke(this.tint).strokeWidth(2.55).opacity(0.45)
        Circle().width(10).height(10).fill(this.tint)
      }.width(19.5).height(19.5).backgroundColor(Colors.surface).borderRadius(10).offset({x:4.75,y:4.75})
      else if (this.activityList && agent.state==='working' && agent.rank===4)
        SymbolGlyph($r('sys.symbol.clock')).fontSize('17vp').fontColor([this.tint]).backgroundColor(Colors.surface).borderRadius(9)
      else if (agent.state && agent.state !== 'unknown') Circle().width(12).height(12).fill(this.tint)
        .stroke(Colors.surface).strokeWidth(2)
    }.width(36*this.controlScale).height(36*this.controlScale)
  }
  @Builder needsReply() {
    Text(tr('needs_you')).fontSize(12).maxFontScale(Tokens.interfaceScaleFor(12)).fontWeight(FontWeight.Medium).fontColor(Colors.needs)
      .padding({left:6,right:6,top:1,bottom:1}).backgroundColor(Colors.selection).borderRadius(6)
      .constraintSize({maxWidth:this.narrow ? '100%' : '41%'}).maxLines(2).textOverflow({overflow:TextOverflow.Ellipsis})
  }
  private previewLeading(): LengthMetrics {
    // The two-line iPhone subtitle adds leading between baselines; a single line keeps its intrinsic height.
    const pointSize = 15*Math.min(this.fontScale,Tokens.interfaceScaleFor(15));
    return LengthMetrics.vp(5+(pointSize-15)/6);
  }
  build() {
    Row({space:12}) {
      this.agentMark(this.agent)
      Column({space:2}) {
        Flex({wrap:FlexWrap.Wrap,alignItems:ItemAlign.Baseline,space:{main:LengthMetrics.vp(8),cross:LengthMetrics.vp(4)}}) {
          Text(this.agent.row.title || tr('agent_untitled')).fontSize(17).maxFontScale(Tokens.interfaceScaleFor(17))
            .fontWeight(this.agent.state === 'unread' ? FontWeight.Bold : 600).fontColor(Colors.ink)
            .constraintSize({maxWidth:'100%'}).flexGrow(1).maxLines(this.narrow ? 4 : 1).textOverflow({overflow:TextOverflow.Ellipsis})
          if (this.age) Text(this.age).fontSize(12).maxFontScale(Tokens.interfaceScaleFor(12)).fontColor(Colors.secondary).maxLines(1)
        }.width('100%')
        if (this.agent.preview) Row({space:8}) {
          Text(this.agent.preview).maxLines(this.narrow ? 4 : 2).fontSize(15).maxFontScale(Tokens.interfaceScaleFor(15)).fontColor(Colors.secondary)
            .lineSpacing(this.previewLeading(),{onlyBetweenLines:true})
            .layoutWeight(1).textOverflow({overflow:TextOverflow.Ellipsis})
          if (this.agent.state === 'needs_you' && !this.narrow) this.needsReply()
        }.width('100%').alignItems(VerticalAlign.Top)
        if (this.agent.state === 'needs_you' && (!this.agent.preview || this.narrow)) this.needsReply()
        ProjectAgentPlaceLine({place:this.place,modelName:this.agent.model,showsSource:this.showsSource,sourceTitle:this.sourceTitle,
          todoTitle:this.agent.todoTitle,sessionId:this.agent.row.id,starter:this.starter,fontScale:this.fontScale})
      }.alignItems(HorizontalAlign.Start).layoutWeight(1)
    }.width('100%').alignItems(VerticalAlign.Top).constraintSize({minHeight:58}).padding({
      left:this.leftInset,right:20,top:19,bottom:19
    })
  }
}
