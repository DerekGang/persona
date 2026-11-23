import { Pressable, StyleSheet, Text, View } from "react-native";
import type { Session } from "@web/lib/types";
import { ago, sessionPreview, sessionTitle, stateOf } from "@web/lib/format";
import { stateName } from "@web/lib/state-labels";
import { font, size, space, stateTone, useTheme } from "../theme";
import { StateChip, Muted } from "./parts";

/** One agent: title, plain state word, its last line, how long ago, and its computer. */
export function AgentRow({ s, machine, onPress }: { s: Session; machine: string; onPress: () => void }) {
  const p = useTheme();
  const st = stateOf(s);
  const preview = sessionPreview(s);
  return (
    <Pressable accessibilityRole="button" accessibilityLabel={[sessionTitle(s), stateName(st), preview, machine].filter(Boolean).join(", ")} onPress={onPress} style={({ pressed }) => [styles.row, { backgroundColor: pressed ? p.raised : p.surface, borderColor: p.line }]}>
      <View style={styles.head}>
        <Text numberOfLines={1} style={[styles.title, { color: p.ink }]}>
          {sessionTitle(s)}
        </Text>
        <Muted style={{ fontSize: font.caption }}>{ago(s.updated_at, Date.now(), "narrow")}</Muted>
      </View>
      <StateChip label={stateName(st)} color={stateTone(p, st)} />
      {preview ? (
        <Text numberOfLines={2} style={{ color: p.ink2, fontSize: font.small }}>
          {preview}
        </Text>
      ) : null}
      {machine ? <Muted style={{ fontSize: font.caption }}>{machine}</Muted> : null}
    </Pressable>
  );
}

const styles = StyleSheet.create({
  row: { padding: space.lg, gap: space.xs, borderBottomWidth: StyleSheet.hairlineWidth, minHeight: size.tap },
  head: { flexDirection: "row", justifyContent: "space-between", gap: space.md },
  title: { flex: 1, fontSize: font.body, fontWeight: "600" },
});
