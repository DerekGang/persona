// BotMux sessions as in Lark (lib/botmux.ts): a message as written there with its tags and its original on request,
// and the replies the agent sent to Lark under an exchange. Drawn while the sidebar's botmux chip decodes
// (lib/botmux-mode.ts); loaded with the first botmux session opened, not with the first screen.
import { memo, type ReactNode, useMemo, useState } from "react";
import { t } from "../../i18n";
import { botmuxReplies, botmuxTags, decodedUser, originalText } from "../../lib/botmux";
import type { Step, UserMessage } from "../../lib/turns";
import type { Botmux } from "../../lib/types";
import { Markdown } from "../../ui/Markdown";

function BotmuxTags({ b, end }: { b: Botmux; end?: boolean }) {
  const tags = botmuxTags(b);
  if (!tags.length) return null;
  return (
    <div className={end ? "flex flex-wrap justify-end gap-1" : "flex flex-wrap gap-1"} data-botmux-tags>
      {tags.map((tag) => (
        <span key={tag} className="rounded-full bg-hover px-1.5 text-[length:var(--chat-secondary)] leading-[18px] text-ink-3 whitespace-nowrap">{tag}</span>
      ))}
    </div>
  );
}

/** "Original": the transcript's words, as the agent read them. */
function BotmuxOriginal({ text, end }: { text: string; end?: boolean }) {
  const [open, setOpen] = useState(false);
  if (!text) return null;
  return (
    <div className={end ? "flex w-full flex-col items-end gap-1" : "flex w-full flex-col items-start gap-1"}>
      <button type="button" className="text-[length:var(--chat-secondary)] text-ink-3 hover:text-ink-2" aria-expanded={open} onClick={() => setOpen((v) => !v)} data-botmux-original>
        {t(open ? "chat-botmux.hide_original" : "chat-botmux.original")}
      </button>
      {open && <pre className="max-h-80 w-full overflow-auto whitespace-pre-wrap break-words rounded-lg bg-hover p-2 font-mono text-[length:var(--chat-secondary)] text-ink-2">{text}</pre>}
    </div>
  );
}

/** What the exchange sent to Lark, each reply as the agent's message; its call stays in the work above. */
export const BotmuxReplies = memo(function BotmuxReplies({ steps, mid }: { steps: readonly Step[]; mid: string }) {
  const replies = useMemo(() => botmuxReplies(steps), [steps]);
  if (!replies.length) return null;
  return (
    <div className="flex flex-col gap-3" data-botmux-replies>
      {replies.map(({ id, reply }) => (
        <div key={id} className="flex min-w-0 flex-col gap-1" data-botmux-reply>
          <BotmuxTags b={reply} />
          <Markdown text={reply.text} mid={mid} />
        </div>
      ))}
    </div>
  );
});

/** A message botmux handed the agent, as written in Lark: its tags, its words in `bubble` (the chat's user bubble), and
 *  the original on request. */
export function BotmuxUser({ msg, bubble }: { msg: UserMessage; bubble: (decoded: UserMessage) => ReactNode }) {
  const decoded = decodedUser(msg);
  if (!decoded) return null;
  return (
    <div className="ml-auto flex max-w-[min(100%,488px)] min-w-0 flex-col items-end gap-1" data-botmux-message={msg.botmux!.kind}>
      <BotmuxTags b={msg.botmux!} end />
      {(decoded.text || decoded.images.length > 0) && bubble(decoded)}
      <BotmuxOriginal text={originalText(msg)} end />
    </div>
  );
}
