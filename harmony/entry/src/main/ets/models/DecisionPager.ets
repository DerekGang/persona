import { Decision } from './Wire';
import { QuestionDraft } from './QuestionWorkspace';

/** The assistant shows an interaction once, even when two supervising tasks refer to the same Agent. */
export function orderedDecisions(own: Decision[], supervised: Decision[]): Decision[] {
  const seen = new Set<string>();
  const result: Decision[] = [];
  for (const request of own.concat(supervised)) {
    if (!request.id || seen.has(request.id) || !['open', 'pending', 'unknown'].includes(request.status)) continue;
    seen.add(request.id);
    result.push(request);
  }
  return result;
}

@ObservedV2
export class DecisionPager {
  @Trace current: string = '';
  @Trace index: number = 0;
  @Trace private foldedIds: string[] = [];
  private scope: string = '';
  private drafts: Map<string, QuestionDraft> = new Map();

  bind(scope: string): void {
    if (scope === this.scope) return;
    this.scope = scope;
    this.current = '';
    this.index = 0;
    this.foldedIds = [];
    this.drafts.clear();
  }

  position(ids: string[]): number {
    const found = ids.indexOf(this.current);
    return found >= 0 ? found : Math.min(this.index, ids.length - 1);
  }

  select(ids: string[], at: number): void {
    if (at < 0 || at >= ids.length) return;
    this.current = ids[at];
    this.index = at;
  }

  reconcile(ids: string[]): void {
    this.select(ids, this.position(ids));
    for (const id of Array.from(this.drafts.keys())) if (!ids.includes(id)) this.drafts.delete(id);
    if (!this.collapsed(ids) && this.foldedIds.length) this.show();
  }

  fold(ids: string[]): void { this.foldedIds = ids.slice(); }
  show(): void { this.foldedIds = []; }
  collapsed(ids: string[]): boolean { return ids.some((id: string) => this.foldedIds.includes(id)); }
  arrived(ids: string[]): number { return this.collapsed(ids) ? ids.filter((id: string) => !this.foldedIds.includes(id)).length : 0; }
  dots(ids: string[]): number[] {
    const start = Math.max(0, Math.min(this.position(ids) - 3, ids.length - 7));
    return ids.slice(start, start + 7).map((_id: string, i: number) => start + i);
  }

  draft(request: Decision): QuestionDraft | undefined {
    if (request.kind !== 'question') return undefined;
    const previous = this.drafts.get(request.id);
    if (previous?.raw === request.raw) return previous;
    const draft = new QuestionDraft(this.scope, request);
    draft.begin(this.scope);
    this.drafts.set(request.id, draft);
    return draft;
  }
}
