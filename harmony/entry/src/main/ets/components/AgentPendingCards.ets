import { Colors } from '../design/Colors';
import { tr } from '../design/Strings';
import { Tokens } from '../design/Tokens';
import { ClientModel } from '../models/ClientModel';
import { agentPendingCards } from '../models/InteractionDecision';
import { DecisionDrafts } from '../models/QuestionWorkspace';
import { Decision } from '../models/Wire';
import { InteractionDecisionForm } from './InteractionDecisionForm';

@ComponentV2
export struct AgentPendingCards {
  @Require @Param model: ClientModel;
  @Require @Param room: number;
  @Param keyboard: boolean = false;
  @Event admission: () => boolean = () => false;
  @Event onLink: (address: string) => void = (_address: string) => {};
  @Local contentHeight: number = 0;
  private list: Scroller = new Scroller();
  private drafts: DecisionDrafts = new DecisionDrafts();
  private previousIds: string[] = [];

  @Computed get cards(): Decision[] { return agentPendingCards(this.model.pendingDecisions, this.model.decision); }
  @Computed get owner(): string { return JSON.stringify([this.model.scope(), this.model.connectionGeneration(), this.model.target()]); }
  @Computed get showCount(): boolean {
    return this.cards.length > 1 && (this.keyboard || this.contentHeight > this.room + 1);
  }

  aboutToAppear(): void { this.updateCards(); }
  @Monitor('cards', 'owner') updateCards(): void {
    const ids = this.cards.map((request: Decision) => request.id);
    this.drafts.bind(this.owner);
    this.drafts.retain(ids);
    if (this.previousIds.length && ids.some((id: string) => !this.previousIds.includes(id))) this.list.scrollEdge(Edge.Top);
    this.previousIds = ids;
  }

  private ticket(request: Decision): () => boolean {
    const owner = this.model, scope = owner.scope(), generation = owner.connectionGeneration(), target = owner.target();
    return (): boolean => this.admission() && this.model === owner && owner.connected && owner.owns(generation, scope) &&
      owner.target() === target && (owner.pendingDecisions.includes(request) || !owner.pendingDecisions.length && owner.decision === request);
  }

  build() {
    Column({ space: 6 }) {
      if (this.showCount) Text(tr('chat_pending_cards_count').replace('{count}', String(this.cards.length)))
        .width('100%').fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).fontColor(Colors.secondary)
        .padding({ left: 4 }).id('chat.pending.count')
      Scroll(this.list) {
        Column({ space: 12 }) {
          ForEach(this.cards, (request: Decision) => {
            Column() {
              InteractionDecisionForm({ model: this.model, request: request, agentCard: true,
                heightLimit: this.cards.length === 1 ? this.room : 0, draft: this.drafts.draft(request), draftSession: this.owner,
                admission: this.ticket(request), onLink: this.onLink })
            }.width('100%').id('chat.pending.card.' + request.id)
          }, (request: Decision) => this.owner + '/' + request.id + '/' + request.raw)
        }.width('100%').padding({ top: 8, bottom: 4 })
          .onAreaChange((_old: Area, area: Area) => {
            const height = Number(area.height);
            if (Number.isFinite(height) && Math.abs(height - this.contentHeight) > 0.5) this.contentHeight = height;
          })
      }.width('100%').align(Alignment.TopStart).scrollBar(BarState.Auto)
        .constraintSize({ maxHeight: this.room }).id('chat.pending.cards')
    }.width('100%').alignItems(HorizontalAlign.Start)
  }
}
