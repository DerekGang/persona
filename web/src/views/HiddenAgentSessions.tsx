import { useEffect, useMemo, useState, useSyncExternalStore } from "react";
import { t } from "../i18n";
import { useScoped } from "../lib/current-assistant";
import { SessionList } from "../lib/session-list";
import { runtimeIdentity, runtimeOrder } from "../lib/runtime-list";
import { href, onLinkClick } from "../lib/router";
import { onHubEvent } from "../lib/store";
import type { Session } from "../lib/types";
import { Button, Dialog, Loading } from "../ui/atoms";
import { Group, Row, RowFailed } from "./settings/SettingsParts";

/** A separate, bounded inspection view: never changes the shared default list or attention counts. */
export function HiddenAgentSessions() {
  const [open, setOpen] = useState(false);
  return <>
    <Button tone="quiet" onClick={() => setOpen(true)}>{t("hidden-agent-dirs.inspect")}</Button>
    <Dialog open={open} onClose={() => setOpen(false)} title={t("hidden-agent-dirs.inspect")} footer={<Button tone="secondary" onClick={() => setOpen(false)}>{t("hidden-agent-dirs.close")}</Button>}>
      {open && <InspectionList close={() => setOpen(false)} />}
    </Dialog>
  </>;
}

function InspectionList({ close }: { close: () => void }) {
  const list = useMemo(() => new SessionList<Session>("/sessions?include_hidden=1", runtimeIdentity, runtimeOrder, undefined, undefined, { counts: false }), []);
  const state = useSyncExternalStore(list.subscribe, list.getSnapshot);
  const rows = useScoped(state.rows);
  useEffect(() => {
    void list.load();
    const off = onHubEvent("sessions_list", () => list.invalidate());
    return () => { off(); list.reset(); };
  }, [list]);
  return <Group footer={t("hidden-agent-dirs.inspect_hint")}>
    {state.error && <RowFailed text={state.error} retry={t("hidden-agent-dirs.retry")} onRetry={() => void list.retry()} />}
    {state.loading && <Row label={<Loading />} />}
    {rows.filter((s) => !s.assistant).map((s) => <Row key={runtimeIdentity(s)} label={<a href={href({ name: "session", mid: s.machine_id, sid: s.session_id })} onClick={(e) => { onLinkClick(e, { name: "session", mid: s.machine_id, sid: s.session_id }); close(); }}>{s.title || t("lib-format.untitled_session")}</a>} hint={t(s.hidden_by_directory ? "hidden-agent-dirs.hidden" : "hidden-agent-dirs.normal")} />)}
    {state.hasMore && <Row label={<Button tone="secondary" busy={state.loading} onClick={() => void list.more()}>{t("hidden-agent-dirs.more")}</Button>} />}
  </Group>;
}
