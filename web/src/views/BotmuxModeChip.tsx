import { t } from "../i18n";
import { nextBotmuxMode, setBotmuxMode, useBotmuxMode } from "../lib/botmux-mode";
import { Icon } from "../ui/icons";

export default function BotmuxModeChip() {
  const mode = useBotmuxMode();
  const isDecoded = mode === "decode";
  return (
    <button
      type="button"
      title={t(isDecoded ? "chat-botmux.show_raw" : "chat-botmux.decode")}
      onClick={() => setBotmuxMode(nextBotmuxMode(mode))}
      data-botmux-mode={mode}
      className="touch-hit relative z-10 flex h-6 items-center gap-1 whitespace-nowrap rounded-full px-[7px] text-[11.5px] text-ink-2 hover:bg-hover hover:text-ink"
    >
      <Icon name={isDecoded ? "check" : "code"} size={10} stroke={2.4} />
      {isDecoded ? "botmux" : t("chat-botmux.raw")}
    </button>
  );
}
