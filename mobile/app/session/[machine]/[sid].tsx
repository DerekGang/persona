import { useLocalSearchParams, Stack } from "expo-router";
import { View } from "react-native";
import { sessionTitle, stateOf } from "@web/lib/format";
import { stateName } from "@web/lib/state-labels";
import { useStore } from "../../../src/runtime/store";
import { ChatView } from "../../../src/ui/ChatView";
import { StateChip, Muted } from "../../../src/ui/parts";
import { m } from "../../../src/i18n";
import { space, stateTone, useTheme } from "../../../src/theme";

export default function SessionScreen() {
  const p = useTheme();
  const { machine, sid } = useLocalSearchParams<{ machine: string; sid: string }>();
  const mid = decodeURIComponent(String(machine));
  const id = decodeURIComponent(String(sid));
  const s = useStore((st) => st.sessions.find((x) => x.machine_id === mid && x.session_id === id));
  const st = s ? stateOf(s) : "unknown";
  return (
    <View style={{ flex: 1, backgroundColor: p.page }}>
      <Stack.Screen options={{ title: s ? sessionTitle(s) : m("session_title") }} />
      {s ? (
        <View style={{ paddingHorizontal: space.lg, paddingVertical: space.sm }}>
          <StateChip label={stateName(st)} color={stateTone(p, st)} />
        </View>
      ) : (
        <Muted style={{ padding: space.lg }}>{m("session_missing")}</Muted>
      )}
      <ChatView mid={mid} sid={id} />
    </View>
  );
}
