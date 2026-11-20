import { useEffect, useId, useRef, useState } from "react";
import { t } from "../../i18n";
import { api, enc, errorText } from "../../lib/api";
import { formatAt, isKind, type KBDetail, type KBEntry, type KBFieldCoverage, type KBMomentRef, type KBProjectKnowledge, type KBUpdate } from "../../lib/kb";
import { href } from "../../lib/router";
import { Button, Switch } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import type { KBAuth } from "./KBDialogs";
import { onPlainClick } from "./KBParts";

type Props = {
  detail: KBDetail;
  knowledge: KBProjectKnowledge;
  auth: KBAuth;
  current: () => boolean;
  onOpen: (entry: KBEntry) => void;
  onMoment: (moment: KBMomentRef) => void;
  onSaved: () => void;
};

function writerLabel(writer?: string): string {
  if (writer === "user") return t("knowledge-projectevidence.by_user");
  if (writer === "pipeline") return t("knowledge-projectevidence.by_persona");
  if (writer?.startsWith("ext:")) return t("knowledge-projectevidence.by_extension", { name: writer.slice(4) });
  return t("knowledge-projectevidence.writer_unknown");
}

function maintainerLabel(maintainer: string): string {
  if (maintainer === "user") return t("knowledge-projectevidence.maintainer_user");
  if (maintainer === "pipeline") return t("knowledge-projectevidence.maintainer_persona");
  if (maintainer === "external") return t("knowledge-projectevidence.maintainer_external");
  if (maintainer.startsWith("ext:")) return t("knowledge-projectevidence.maintainer_extension", { name: maintainer.slice(4) });
  return t("knowledge-projectevidence.maintainer_unknown");
}

function Coverage({ title, field, position }: { title: string; field: KBFieldCoverage; position: number }) {
  return (
    <div className="min-w-0">
      <dt className="flex flex-wrap items-center gap-x-2 gap-y-1 text-sm font-medium text-ink">
        {title}
        {field.locked && <span className="inline-flex items-center gap-1 text-xs font-normal text-ink-2"><Icon name="lock" size={12} />{t("knowledge-projectevidence.locked")}</span>}
      </dt>
      <dd className="mt-1 text-xs leading-relaxed text-ink-2">
        {field.cutoff === undefined ? t("knowledge-projectevidence.unknown") : t("knowledge-projectevidence.cutoff", { cutoff: field.cutoff, count: position })}
        <span aria-hidden="true"> · </span>{writerLabel(field.writer)}
      </dd>
      {field.maintainer && <dd className="mt-0.5 text-xs leading-relaxed text-ink-2">{maintainerLabel(field.maintainer)}</dd>}
      {!!field.source_refs?.length && <dd className="mt-1 text-xs leading-relaxed break-all text-ink-3">{t("knowledge-projectevidence.based_on", { refs: field.source_refs.join(t("knowledge-projectevidence.join")) })}</dd>}
    </div>
  );
}

function sourceState(value: string): string {
  switch (value) {
    case "open": return t("knowledge-projectevidence.open");
    case "merged": return t("knowledge-projectevidence.merged");
    case "closed": return t("knowledge-projectevidence.closed");
    default: return value;
  }
}

function SourceRef({ value, detail, onOpen, onMoment }: { value: string } & Pick<Props, "detail" | "onOpen" | "onMoment">) {
  const style = "break-all text-accent-ink underline underline-offset-2";
  if (/^https?:\/\//i.test(value)) return <a className={style} href={value} target="_blank" rel="noopener noreferrer">{value}</a>;
  const slash = value.indexOf("/");
  const kind = value.slice(0, slash);
  const id = value.slice(slash + 1);
  const moment = kind === "moments" && detail.moments?.find(m => m.id === id);
  if (moment) return <button type="button" className={style} onClick={() => onMoment(moment)}>{value}</button>;
  // Local evidence such as projects/<id>/log identifies a field, not a separate entry to open.
  if (slash < 0 || !isKind(kind) || !id || id.includes("/")) return <span className="break-all">{value}</span>;
  const entry = detail.links?.find(e => e.kind === kind && e.id === id) ?? { kind, id, name: value, relevance: 0 };
  return <span><a className={style} href={href({ name: "kb", kind, id })} onClick={e => onPlainClick(e, () => onOpen(entry))}>{entry.name}</a>
    {entry.name !== value && <span className="ml-2 break-all text-ink-3">{value}</span>}</span>;
}

export function ProjectEvidence({ detail, knowledge, auth, current, onOpen, onMoment, onSaved }: Props) {
  const [editing, setEditing] = useState(false);
  const refs = Array.isArray(detail.meta?.source_refs) ? detail.meta.source_refs.filter((ref): ref is string => typeof ref === "string") : [];
  return (
    <details className="group rounded-xl border border-line bg-surface">
      <summary className="flex cursor-pointer list-none items-center gap-2 rounded-xl px-4 py-3 text-sm font-medium text-ink hover:bg-hover [&::-webkit-details-marker]:hidden">
        <Icon name="book" size={16} className="shrink-0 text-ink-3" />
        <span className="min-w-0 flex-1">{t("knowledge-projectevidence.title")}</span>
        {knowledge.policy === "reference_only" && <span className="text-xs font-normal text-ink-2">{t("knowledge-projectevidence.references_only")}</span>}
        <Icon name="chevron" size={13} className="shrink-0 text-ink-3 transition-transform group-open:rotate-180" />
      </summary>
      <div className="space-y-4 border-t border-line px-4 py-4">
        <dl className="grid gap-4 sm:grid-cols-2">
          <Coverage title={t("knowledge-projectevidence.summary")} field={knowledge.fields.summary} position={knowledge.position} />
          <Coverage title={t("knowledge-projectevidence.overview")} field={knowledge.fields.one_line} position={knowledge.position} />
        </dl>
        {!!knowledge.sources?.length && (
          <section>
            <h3 className="mb-1.5 text-xs font-medium text-ink-2">{t("knowledge-projectevidence.sources")}</h3>
            <ul className="space-y-1.5 text-xs leading-relaxed">
              {knowledge.sources.map(source => <li key={source.ref}>
                <SourceRef value={source.ref} detail={detail} onOpen={onOpen} onMoment={onMoment} />
                {source.state && <span className="block text-ink-2">{t("knowledge-projectevidence.source_state", { state: sourceState(source.state) })}
                  {source.state_at && <time dateTime={source.state_at}> · {formatAt(source.state_at)}</time>}</span>}
              </li>)}
            </ul>
          </section>
        )}
        <div className="border-t border-line pt-3">
          {editing ? <StorageEditor id={detail.id} policy={knowledge.policy} refs={refs} auth={auth} current={current} onClose={() => setEditing(false)} onSaved={onSaved} /> : (
            <div className="flex flex-wrap items-start justify-between gap-2">
              <div className="min-w-0 flex-1">
                <h3 className="text-sm font-medium text-ink">{t(knowledge.policy === "reference_only" ? "knowledge-projectevidence.references_only" : "knowledge-projectevidence.managed")}</h3>
                <p className="mt-1 text-xs leading-relaxed text-ink-2">{t(knowledge.policy === "reference_only" ? "knowledge-projectevidence.reference_hint" : "knowledge-projectevidence.managed_hint")}</p>
              </div>
              <Button tone="secondary" onClick={() => current() && setEditing(true)}>{t("knowledge-projectevidence.edit")}</Button>
            </div>
          )}
        </div>
      </div>
    </details>
  );
}

function StorageEditor({ id, policy, refs, auth, current, onClose, onSaved }: {
  id: string; policy: KBProjectKnowledge["policy"]; refs: string[]; auth: KBAuth;
  current: () => boolean; onClose: () => void; onSaved: () => void;
}) {
  // Editing starts from explicit pointers only. Linked chats in project_knowledge.sources have their own relationship.
  const [initial] = useState(() => ({ policy, refs }));
  const [nextPolicy, setPolicy] = useState(policy);
  const [draft, setDraft] = useState(() => refs.join("\n"));
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);
  const busy = useRef(false);
  const alive = useRef(true);
  useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
  const valid = () => alive.current && current();
  const label = useId();
  const nextRefs = draft.split(/\r?\n/).map(ref => ref.trim()).filter(Boolean);
  const refsChanged = nextRefs.length !== initial.refs.length || nextRefs.some((ref, i) => ref !== initial.refs[i]);
  const changed = nextPolicy !== initial.policy || refsChanged;
  const save = async () => {
    if (!valid() || busy.current || !changed) return;
    busy.current = true;
    setSaving(true);
    setError("");
    const update: KBUpdate = {};
    if (nextPolicy !== initial.policy) update.storage_policy = nextPolicy;
    if (refsChanged) update.source_refs = nextRefs;
    try {
      await api(`/kb/projects/${enc(id)}`, { method: "PUT", json: update, ...auth });
      if (valid()) { onSaved(); onClose(); }
    } catch (err) {
      if (valid()) setError(errorText(err));
    } finally {
      busy.current = false;
      if (valid()) setSaving(false);
    }
  };
  return (
    <form className="space-y-3" onSubmit={e => { e.preventDefault(); void save(); }}>
      <div className="flex items-start gap-3">
        <div className="min-w-0 flex-1">
          <p className="text-sm font-medium text-ink">{t("knowledge-projectevidence.references_only")}</p>
          <p id={`${label}-hint`} className="mt-1 text-xs leading-relaxed text-ink-2">{t("knowledge-projectevidence.reference_hint")}</p>
        </div>
        <Switch label={t("knowledge-projectevidence.references_only")} describedBy={`${label}-hint`} on={nextPolicy === "reference_only"} onChange={on => setPolicy(on ? "reference_only" : "managed")} disabled={saving} />
      </div>
      <div>
        <label htmlFor={label} className="text-xs font-medium text-ink">{t("knowledge-projectevidence.pointers")}</label>
        <p id={`${label}-format`} className="mt-0.5 text-xs text-ink-2">{t("knowledge-projectevidence.pointer_hint")}</p>
        <textarea id={label} aria-describedby={`${label}-format`} className="mt-1.5 w-full resize-y rounded-lg border border-line bg-field px-3 py-2 text-sm text-ink" rows={3} value={draft} disabled={saving} onChange={e => setDraft(e.target.value)} />
      </div>
      {error && <p role="alert" className="text-xs text-red">{error}</p>}
      <div className="flex flex-wrap justify-end gap-2">
        <Button tone="quiet" disabled={saving} onClick={onClose}>{t("common.cancel")}</Button>
        <Button type="submit" tone="accent" busy={saving} disabled={!changed || saving}>{t("common.save")}</Button>
      </div>
    </form>
  );
}
