// Arranging Overview's extension cards (#9745, docs/web/web.md "Overview's extension cards"), loaded the first time the
// user presses Arrange cards: the heading over the cards (what to do, where it is kept, Reset with Undo, Done) and each
// card's controls in place of its head (the handle it is dragged by, its width, earlier / later, its switch). The
// patterns are the sidebar's pages' (Settings › General › Sidebar pages, views/settings/SidebarPages.tsx): a drag with
// ↑ / ↓ beside it for the keyboard and touch, a switch to show or hide, a native select, the same footer about the hub.
import { t } from "../../i18n";
import type { UIPlace } from "../../lib/extension-ui";
import { getOverviewPrefs, resetOverview, saveOverview, useOverviewKept, useOverviewPrefs, type CardSpan } from "../../lib/overview-cards";
import { useStartPageSynced } from "../../lib/start-page";
import { Button, cx, IconButton, Switch, toast } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import type { CardEdit } from "./ExtensionSlots";

const FIELD = "h-8 min-w-0 rounded-control bg-field px-2.5 text-meta text-ink shadow-inset-field outline-none focus:shadow-[inset_0_0_0_1px_var(--accent)]";

/** Over the cards while they are arranged: what to do, whether every device follows, Reset (with Undo) and Done. */
export function OverviewEditHead({ onDone }: { onDone: () => void }) {
  const chosen = useOverviewPrefs().length > 0;
  const route = useStartPageSynced();
  const field = useOverviewKept();
  const synced = route !== false && field !== false;
  const reset = () => {
    const before = getOverviewPrefs();
    resetOverview();
    toast(t("extensions-overviewarrange.reset_done"), "info", { label: t("extensions-overviewarrange.undo"), run: () => saveOverview(before) });
  };
  return (
    <div data-overview-arrange className="col-span-full flex flex-wrap items-start gap-x-3 gap-y-2">
      <div className="min-w-48 flex-1">
        <h2 className="text-[13px] font-medium text-ink">{t("extensions-overviewarrange.title")}</h2>
        <p className="mt-0.5 text-[12.5px] leading-[1.5] text-ink-2">
          {t("extensions-overviewarrange.line", { hint: t("extensions-overviewarrange.hint"), where: synced ? t("settings.start_page_footer_synced") : t("settings.start_page_footer_local") })}
        </p>
      </div>
      <div className="ml-auto flex items-center gap-2">
        {chosen && (
          <Button tone="quiet" size="sm" onClick={reset}>
            {t("extensions-overviewarrange.reset")}
          </Button>
        )}
        <Button tone="primary" size="sm" onClick={onDone}>
          {t("extensions-overviewarrange.done")}
        </Button>
      </div>
    </div>
  );
}

/** A card's controls while the cards are arranged, in place of its head: its handle and name, its width, earlier /
 *  later, and its switch (off: hidden, so Overview draws nothing of it and does not ask for it). */
export function OverviewEditBar({ place, span, hidden, edit }: { place: UIPlace; span: CardSpan; hidden: boolean; edit: CardEdit }) {
  const name = place.title;
  return (
    // the name wraps beside the controls on a wide card and the controls go under it on a narrow one, rather than
    // cutting it short
    <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1.5 px-3 pt-2.5 pb-1">
      <span aria-hidden className="cursor-grab text-ink-3 active:cursor-grabbing" title={t("extensions-overviewarrange.drag")}>
        <Icon name="grip" size={14} />
      </span>
      <div className="min-w-24 flex-1">
        <div className={cx("truncate text-[13px] font-medium", hidden ? "text-ink-3" : "text-ink")}>{name}</div>
        {(hidden || place.extension_name) && <div className="truncate text-[11px] text-ink-3">{hidden ? t("extensions-overviewarrange.hidden") : place.extension_name}</div>}
      </div>
      <div className="ml-auto flex items-center gap-1.5">
        <select value={span} onChange={(e) => edit.set({ span: e.target.value === "full" ? "full" : "half" })} aria-label={t("extensions-overviewarrange.width", { name })} className={cx(FIELD, "max-w-32")}>
          <option value="half">{t("extensions-overviewarrange.half")}</option>
          <option value="full">{t("extensions-overviewarrange.full")}</option>
        </select>
        <IconButton icon="up" label={t("extensions-overviewarrange.earlier", { name })} disabled={edit.first} onClick={() => edit.move(-1)} size={26} iconSize={13} />
        <IconButton icon="arrowDown" label={t("extensions-overviewarrange.later", { name })} disabled={edit.last} onClick={() => edit.move(1)} size={26} iconSize={13} />
        <Switch on={!hidden} onChange={(on) => edit.set({ hidden: !on })} label={t("extensions-overviewarrange.show", { name })} />
      </div>
    </div>
  );
}
