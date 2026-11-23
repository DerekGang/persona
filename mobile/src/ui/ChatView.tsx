// A conversation: bubbles, a live reply, pending requests over the composer, and the composer. Used by the main chat and
// by an agent's session.
import { useEffect, useMemo } from "react";
import { FlatList, KeyboardAvoidingView, Platform, Pressable, StyleSheet, Text, View } from "react-native";
import { loadEarlier, openChat, retryMessage, sendMessage } from "../runtime/actions";
import { chatKey, emptyChat, useStore } from "../runtime/store";
import { transcriptRows, type Row } from "../domain/transcript";
import { m } from "../i18n";
import { font, radius, space, useTheme } from "../theme";
import { Composer } from "./Composer";
import { QuestionCard } from "./QuestionCard";
import { Muted } from "./parts";

export function ChatView({ mid, sid }: { mid: string; sid: string }) {
  const p = useTheme();
  const chat = useStore((s) => s.chats[chatKey(mid, sid)]) ?? emptyChat();
  const pending = useStore((s) => s.interactions);
  const asks = useMemo(() => pending.filter((i) => i.machine_id === mid && i.session_id === sid), [pending, mid, sid]);
  const rows = useMemo(() => transcriptRows(chat.items), [chat.items]);
  useEffect(() => openChat(mid, sid), [mid, sid]);
  const data = useMemo(() => [...rows].reverse(), [rows]); // inverted list: newest first

  const send = (text: string) => void sendMessage(mid, sid, text);
  return (
    <KeyboardAvoidingView style={{ flex: 1, backgroundColor: p.page }} behavior={Platform.OS === "ios" ? "padding" : undefined}>
      <FlatList
        inverted
        data={data}
        keyExtractor={(r) => r.id}
        contentContainerStyle={styles.list}
        ListHeaderComponent={chat.live ? <Bubble role="assistant" text={chat.live} note={m("writing")} /> : undefined}
        ListFooterComponent={chat.hasMore ? <EarlierButton onPress={() => void loadEarlier(mid, sid)} /> : undefined}
        ListEmptyComponent={chat.loaded ? <Muted style={styles.empty}>{m("chat_empty")}</Muted> : undefined}
        renderItem={({ item }) => <RowView row={item} state={item.kind === "message" && item.clientId ? chat.sends[item.clientId] : undefined} onRetry={(id) => retryMessage(mid, sid, id)} />}
      />
      {asks.length ? (
        <View style={styles.asks}>
          {asks.map((ix) => (
            <QuestionCard key={ix.id} ix={ix} />
          ))}
        </View>
      ) : undefined}
      <Composer placeholder={mid && sid ? m("composer_placeholder") : ""} onSend={send} />
    </KeyboardAvoidingView>
  );
}

function EarlierButton({ onPress }: { onPress: () => void }) {
  const p = useTheme();
  return (
    <Pressable onPress={onPress} accessibilityRole="button" style={styles.earlier}>
      <Text style={{ color: p.accent, fontSize: font.small }}>{m("load_earlier")}</Text>
    </Pressable>
  );
}

function RowView({ row, state, onRetry }: { row: Row; state?: string; onRetry: (id: string) => void }) {
  const p = useTheme();
  if (row.kind === "tools") {
    const names = row.names.length > 2 ? `${row.names.slice(0, 2).join(", ")} +${row.names.length - 2}` : row.names.join(", ");
    return <Muted style={styles.tools}>{m("tool_line", { tool: names })}</Muted>;
  }
  const note = state === "sending" ? m("sending") : state === "processing" ? m("processing") : state === "failed" ? m("failed_retry") : state === "sent" ? m("sent") : undefined;
  return (
    <View>
      <Bubble role={row.role} text={row.text} note={note} noteColor={state === "failed" ? p.danger : undefined} onNote={state === "failed" && row.clientId ? () => onRetry(row.clientId!) : undefined} />
    </View>
  );
}

function Bubble({ role, text, note, noteColor, onNote }: { role: "user" | "assistant"; text: string; note?: string; noteColor?: string; onNote?: () => void }) {
  const p = useTheme();
  const mine = role === "user";
  return (
    <View style={[styles.bubbleWrap, { alignItems: mine ? "flex-end" : "flex-start" }]}>
      <View style={[styles.bubble, { backgroundColor: mine ? p.accent : p.surface, borderColor: p.line, borderWidth: mine ? 0 : StyleSheet.hairlineWidth }]}>
        <Text selectable style={{ color: mine ? p.onAccent : p.ink, fontSize: font.body }}>
          {text}
        </Text>
      </View>
      {note ? (
        <Pressable disabled={!onNote} onPress={onNote} accessibilityRole={onNote ? "button" : "text"}>
          <Text style={{ color: noteColor ?? p.muted, fontSize: font.caption, marginTop: space.xs }}>{note}</Text>
        </Pressable>
      ) : undefined}
    </View>
  );
}

const styles = StyleSheet.create({
  list: { padding: space.lg, gap: space.sm },
  bubbleWrap: { marginVertical: space.xs },
  bubble: { maxWidth: "86%", borderRadius: radius.lg, paddingHorizontal: space.md, paddingVertical: space.sm },
  tools: { marginVertical: space.xs },
  empty: { textAlign: "center", marginTop: space.xxl },
  asks: { paddingHorizontal: space.lg, paddingBottom: space.sm, gap: space.sm },
  earlier: { alignItems: "center", padding: space.md },
});
