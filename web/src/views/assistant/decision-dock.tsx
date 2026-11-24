import { useEffect, useMemo, useRef, useState } from "react";
import { decisionKey, decisionPosition, type DecisionCard } from "../../lib/decision-pager";
import type { QuestionDraft } from "../chat/decision-card";

export function useDecisionPager(cards: readonly DecisionCard[]) {
  const keys = useMemo(() => cards.map(({ interaction }) => decisionKey(interaction)), [cards]);
  const [selection, setSelection] = useState<{ key?: string; index: number }>({ index: 0 });
  const [folded, setFolded] = useState<ReadonlySet<string>>(new Set());
  const drafts = useRef(new Map<string, QuestionDraft>()).current;
  const at = decisionPosition(keys, selection.key, selection.index);
  const rememberedIndex = Math.max(at, 0);
  const key = keys[at];
  const collapsed = keys.some((candidate) => folded.has(candidate));

  useEffect(() => {
    if (selection.key !== key || selection.index !== rememberedIndex) setSelection({ key, index: rememberedIndex });
    if (!collapsed && folded.size) setFolded(new Set());
    for (const draft of drafts.keys()) if (!keys.includes(draft)) drafts.delete(draft);
  }, [collapsed, drafts, folded.size, key, keys, rememberedIndex, selection.index, selection.key]);

  function go(index: number) {
    if (keys[index] !== undefined) setSelection({ key: keys[index], index });
  }

  function reveal(id: string) {
    const index = cards.findIndex(({ interaction }) => interaction.id === id);
    if (index < 0) return false;
    go(index);
    setFolded(new Set());
    return true;
  }

  return {
    at, key, card: cards[at], count: keys.length, collapsed, drafts, go, reveal,
    arrived: collapsed ? keys.filter((candidate) => !folded.has(candidate)).length : 0,
    open: () => setFolded(new Set()),
    collapse: () => setFolded(new Set(keys)),
  };
}

export type Pager = ReturnType<typeof useDecisionPager>;
