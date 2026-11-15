import { useEffect, useRef, useState } from "react";
import { api, errorText } from "../../lib/api";
import { useScopedMachines } from "../../lib/current-assistant";
import { composing } from "../../lib/send-history";
import { t } from "../../i18n";
import { Button, Loading } from "../../ui/atoms";
import { Group, Row, RowFailed, SettingsHint, Switch, settingsInputClass } from "./SettingsParts";

export interface HiddenAgentDirRule { machine_id: string; pattern: string; enabled: boolean }
export interface HiddenAgentDirsState { rules: HiddenAgentDirRule[] }
const endpoint = "/assistant/hidden-agent-dirs";

/** Visibility only, shared by every runtime; offline computers can be configured too. */
export function HiddenAgentDirs() {
  const machines = useScopedMachines();
  const [mid, setMid] = useState("");
  const machine = mid || machines[0]?.id || "";
  const [state, setState] = useState<HiddenAgentDirsState | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const saving = useRef(false);
  const [input, setInput] = useState("");
  const [reload, setReload] = useState(0);
  useEffect(() => {
    let dead = false;
    setError("");
    api<HiddenAgentDirsState>(endpoint).then((v) => { if (!dead) setState({ rules: v.rules ?? [] }); }).catch((e) => { if (!dead) setError(errorText(e)); });
    return () => { dead = true; };
  }, [reload]);
  const save = async (rules: HiddenAgentDirRule[]) => {
    if (!state || saving.current) return false;
    const previous = state;
    saving.current = true;
    setBusy(true);
    setError("");
    setState({ rules });
    try {
      setState(await api<HiddenAgentDirsState>(endpoint, { method: "PUT", json: { rules } }));
      return true;
    } catch (e) {
      setState(previous);
      setError(errorText(e));
      return false;
    } finally { saving.current = false; setBusy(false); }
  };
  const add = async () => {
    const pattern = input.trim();
    if (!state || !machine || !pattern || saving.current) return;
    if (await save([...state.rules, { machine_id: machine, pattern, enabled: true }])) setInput("");
  };
  return <Group title={t("hidden-agent-dirs.title")} footer={t("hidden-agent-dirs.footer")}>
    <Row label={t("hidden-agent-dirs.machine")}>
      <select aria-label={t("hidden-agent-dirs.machine")} value={machine} onChange={(e) => setMid(e.target.value)} disabled={busy} className={settingsInputClass}>
        {machines.map((m) => <option key={m.id} value={m.id}>{m.name || m.id}</option>)}
      </select>
    </Row>
    {error && <RowFailed text={t("hidden-agent-dirs.failed", { error })} retry={t("hidden-agent-dirs.retry")} onRetry={() => setReload((n) => n + 1)} />}
    {!state ? <Row label={<Loading />} /> : <>
      {state.rules.map((rule, i) => rule.machine_id !== machine ? null : <Row key={`${rule.machine_id}/${rule.pattern}`} label={rule.pattern} hint={t(rule.enabled ? "hidden-agent-dirs.enabled" : "hidden-agent-dirs.disabled")}>
        <Switch label={rule.pattern} on={rule.enabled} disabled={busy} onChange={(enabled) => void save(state.rules.map((r, n) => n === i ? { ...r, enabled } : r))} />
        <Button tone="quiet" disabled={busy} onClick={() => void save(state.rules.filter((_, n) => n !== i))}>{t("hidden-agent-dirs.remove")}</Button>
      </Row>)}
      {!state.rules.some((r) => r.machine_id === machine) && <SettingsHint>{t("hidden-agent-dirs.empty")}</SettingsHint>}
      <Row label={t("hidden-agent-dirs.pattern")} stackOnNarrow>
        <input className={settingsInputClass} aria-label={t("hidden-agent-dirs.pattern")} placeholder="~/workspaces/*" value={input} onChange={(e) => setInput(e.target.value)} onKeyDown={(e) => { if (e.key === "Enter" && !composing(e.nativeEvent)) void add(); }} spellCheck={false} autoComplete="off" />
        <Button tone="secondary" disabled={busy || !machine || !input.trim()} busy={busy} onClick={() => void add()}>{t("hidden-agent-dirs.add")}</Button>
      </Row>
    </>}
  </Group>;
}
