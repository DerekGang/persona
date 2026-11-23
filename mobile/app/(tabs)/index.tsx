import { StyleSheet, Text, View } from "react-native";
import { useStore } from "../../src/runtime/store";
import { ChatView } from "../../src/ui/ChatView";
import { m } from "../../src/i18n";
import { font, size, space, useTheme } from "../../src/theme";

/** The main chat: the assistant's conversation, found through the hub's /assistant (its `main` id). */
export default function MainChat() {
  const p = useTheme();
  const main = useStore((s) => s.main);
  const conn = useStore((s) => s.conn);
  const tone = conn === "online" ? p.working : conn === "offline" ? p.danger : p.muted;
  return (
    <View style={{ flex: 1, backgroundColor: p.page }}>
      <View style={styles.status}>
        <View style={[styles.dot, { backgroundColor: tone }]} />
        <Text style={{ color: p.muted, fontSize: font.caption }}>{m(`conn_${conn}`)}</Text>
      </View>
      {main ? <ChatView mid={main.mid} sid={main.sid} /> : <Text style={{ color: p.muted, padding: space.xl }}>{m("chat_loading")}</Text>}
    </View>
  );
}

const styles = StyleSheet.create({
  status: { flexDirection: "row", alignItems: "center", gap: space.xs, paddingHorizontal: space.lg, paddingVertical: space.xs },
  dot: { width: size.dot, height: size.dot, borderRadius: size.dot },
});
