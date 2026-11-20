// The next request waiting in another chat, after this chat's own cards are answered (#4091).
// Agent chats use the standalone row; the Assistant places the same link in its one-line action bar (#7770).
import { sessionTitle } from "../../lib/format";
import { plainMarkdown } from "../../lib/plain-markdown";
import { cardState } from "../../lib/card-focus";
import { href, onLinkClick, type Route } from "../../lib/router";
import { useStore } from "../../lib/store";
import { t } from "../../i18n";
import { cx, EASE } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { cardLine, nextWaiting } from "./dock";

/** "N more need you · Next: <agent> · <what it asks> ›", opening that chat with that card first (lib/card-focus.ts).
 *  Mounted only right after an answer here: cards in other chats redraw the link, not this chat's scroll position. */
export function NextWaitingRow({ mid, sid, compact = false, className }: { mid: string; sid: string; compact?: boolean; className?: string }) {
  const next = useStore((s) => nextWaiting(s.interactions, mid, sid));
  const session = useStore((s) => (next ? (s.sessions[next.ix.machine_id] ?? []).find((x) => x.session_id === next.ix.session_id) : undefined));
  if (!next) return null;
  const { ix, count } = next;
  const title = ix.session_title?.trim() || (session ? sessionTitle(session) : ix.cwd || t("home.session"));
  const ask = plainMarkdown(cardLine(ix)).replace(/\s+/g, " ").trim();
  const to: Route = { name: "session", mid: ix.machine_id, sid: ix.session_id };
  return (
    <a
      href={href(to)}
      data-chat-next-waiting
      data-compact={compact || undefined}
      onClick={(e) => onLinkClick(e, to, cardState(ix.id))}
      className={cx("shrink-0 items-center gap-2 transition-colors hover:bg-hover", compact ? className : "flex h-11 rounded-card bg-surface px-3.5 shadow-card")}
      style={compact ? undefined : { animation: `fade-up 250ms ${EASE} both` }}
    >
      <span className={cx("shrink-0 rounded-full bg-attention", compact ? "size-2" : "size-1.5")} />
      <span className={cx("shrink-0 font-medium whitespace-nowrap text-ink", !compact && "text-[13px]")}>{t("chat-interactions.next_waiting", { count })}</span>
      <span className={cx("min-w-0 flex-1 truncate text-ink-3", !compact && "text-[12.5px]")}>{t("chat-interactions.next_waiting_detail", { title, ask })}</span>
      <Icon name="chevronRight" size={14} stroke={2.2} className="shrink-0 text-ink-3" />
    </a>
  );
}
