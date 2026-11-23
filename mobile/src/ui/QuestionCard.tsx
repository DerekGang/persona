// A pending request of an agent: a question (options, several choices, or your own words), a plan to approve, or a
// permission. The demo draws these three; the web's cards cover more kinds (screens, elicitation forms, secrets).
import { useState } from "react";
import { Pressable, StyleSheet, Text, TextInput, View } from "react-native";
import type { Interaction, Question } from "@web/lib/types";
import { optionDisplay } from "@web/lib/tool-detail";
import { resolveInteraction } from "../runtime/actions";
import { m } from "../i18n";
import { font, radius, size, space, useTheme } from "../theme";
import { Button, Card, Muted } from "./parts";

export function QuestionCard({ ix }: { ix: Interaction }) {
  const p = useTheme();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const run = async (a: Parameters<typeof resolveInteraction>[1]) => {
    setBusy(true);
    const err = await resolveInteraction(ix, a);
    setError(err);
    setBusy(false);
  };
  return (
    <Card style={{ borderColor: p.needs }}>
      {ix.kind === "question" ? (
        <Questions ix={ix} busy={busy} onAnswer={(answers) => run({ kind: "question", answers })} />
      ) : ix.kind === "plan" ? (
        <Plan ix={ix} busy={busy} onApprove={() => run({ kind: "plan", approve: true })} onChanges={(feedback) => run({ kind: "plan", approve: false, feedback })} />
      ) : (
        <Permission ix={ix} busy={busy} onAllow={(allow) => run({ kind: "permission", allow })} />
      )}
      {error ? <Text style={{ color: p.danger, fontSize: font.small }}>{error}</Text> : null}
    </Card>
  );
}

function Questions({ ix, busy, onAnswer }: { ix: Interaction; busy: boolean; onAnswer: (a: Record<string, string>) => void }) {
  const qs = ix.questions ?? [];
  const [picked, setPicked] = useState<Record<number, string[]>>({});
  const [custom, setCustom] = useState<Record<number, string>>({});
  const [other, setOther] = useState<Record<number, boolean>>({});
  const answerOf = (i: number) => (other[i] ? (custom[i] ?? "").trim() : (picked[i] ?? []).join(", "));
  const ready = qs.length > 0 && qs.every((_, i) => answerOf(i).length > 0);
  return (
    <View style={{ gap: space.md }}>
      {qs.map((q, i) => (
        <QuestionBlock
          key={i}
          q={q}
          picked={picked[i] ?? []}
          other={!!other[i]}
          custom={custom[i] ?? ""}
          onPick={(label) =>
            setPicked((s) => {
              const cur = s[i] ?? [];
              const next = q.multi_select ? (cur.includes(label) ? cur.filter((x) => x !== label) : [...cur, label]) : [label];
              return { ...s, [i]: next };
            })
          }
          onOther={(on) => setOther((s) => ({ ...s, [i]: on }))}
          onCustom={(v) => setCustom((s) => ({ ...s, [i]: v }))}
        />
      ))}
      <Button
        label={m("answer_send")}
        busy={busy}
        disabled={!ready}
        onPress={() => onAnswer(Object.fromEntries(qs.map((q, i) => [q.question, answerOf(i)])))}
      />
    </View>
  );
}

function QuestionBlock(props: { q: Question; picked: string[]; other: boolean; custom: string; onPick: (l: string) => void; onOther: (on: boolean) => void; onCustom: (v: string) => void }) {
  const p = useTheme();
  const { q } = props;
  const options = q.options ?? [];
  return (
    <View style={{ gap: space.sm }}>
      {q.header ? <Muted>{q.header}</Muted> : null}
      <Text style={{ color: p.ink, fontSize: font.body, fontWeight: "600" }}>{q.question}</Text>
      {options.map((o) => {
        const on = !props.other && props.picked.includes(o.label);
        return (
          <Pressable
            key={o.label}
            accessibilityRole={q.multi_select ? "checkbox" : "radio"}
            accessibilityState={{ selected: on }}
            onPress={() => {
              props.onOther(false);
              props.onPick(o.label);
            }}
            style={[styles.option, { borderColor: on ? p.accent : p.line, backgroundColor: on ? p.raised : "transparent" }]}
          >
            <Text style={{ color: p.ink, fontSize: font.body }}>{optionDisplay(o.label).text}</Text>
            {o.description ? <Muted>{o.description}</Muted> : null}
          </Pressable>
        );
      })}
      <Pressable accessibilityRole="radio" accessibilityState={{ selected: props.other }} onPress={() => props.onOther(true)} style={[styles.option, { borderColor: props.other ? p.accent : p.line }]}>
        <Text style={{ color: p.ink, fontSize: font.body }}>{m("question_other")}</Text>
      </Pressable>
      {props.other || options.length === 0 ? (
        <TextInput
          value={props.custom}
          onChangeText={(v) => {
            props.onOther(true);
            props.onCustom(v);
          }}
          placeholder={m("question_other_placeholder")}
          placeholderTextColor={p.muted}
          style={[styles.input, { color: p.ink, backgroundColor: p.raised }]}
        />
      ) : null}
    </View>
  );
}

function Plan({ ix, busy, onApprove, onChanges }: { ix: Interaction; busy: boolean; onApprove: () => void; onChanges: (feedback: string) => void }) {
  const p = useTheme();
  const [asking, setAsking] = useState(false);
  const [feedback, setFeedback] = useState("");
  return (
    <View style={{ gap: space.md }}>
      <Text style={{ color: p.ink, fontSize: font.body, fontWeight: "600" }}>{m("plan_title")}</Text>
      <Text style={{ color: p.ink2, fontSize: font.small }}>{ix.plan}</Text>
      {asking ? (
        <>
          <TextInput value={feedback} onChangeText={setFeedback} placeholder={m("plan_feedback_placeholder")} placeholderTextColor={p.muted} multiline style={[styles.input, { color: p.ink, backgroundColor: p.raised }]} />
          <Button label={m("plan_changes")} tone="quiet" busy={busy} disabled={!feedback.trim()} onPress={() => onChanges(feedback.trim())} />
        </>
      ) : (
        <Button label={m("plan_changes")} tone="quiet" busy={busy} onPress={() => setAsking(true)} />
      )}
      <Button label={m("plan_approve")} busy={busy} onPress={onApprove} />
    </View>
  );
}

function Permission({ ix, busy, onAllow }: { ix: Interaction; busy: boolean; onAllow: (allow: boolean) => void }) {
  const p = useTheme();
  return (
    <View style={{ gap: space.md }}>
      <Text style={{ color: p.ink, fontSize: font.body, fontWeight: "600" }}>{m("permission_title")}</Text>
      <Text style={{ color: p.ink2, fontSize: font.small }}>{ix.summary || String(ix.tool_input?.command ?? ix.tool_name ?? "")}</Text>
      <View style={{ flexDirection: "row", gap: space.sm }}>
        <View style={{ flex: 1 }}>
          <Button label={m("permission_deny")} tone="quiet" busy={busy} onPress={() => onAllow(false)} />
        </View>
        <View style={{ flex: 1 }}>
          <Button label={m("permission_allow")} busy={busy} onPress={() => onAllow(true)} />
        </View>
      </View>
    </View>
  );
}

const styles = StyleSheet.create({
  option: { borderWidth: StyleSheet.hairlineWidth, borderRadius: radius.md, padding: space.md, minHeight: size.tap, gap: space.xs, justifyContent: "center" },
  input: { borderRadius: radius.md, padding: space.md, fontSize: font.body, minHeight: size.tap },
});
