import { useMemo, useState } from "react";
import { RefreshControl, SectionList, StyleSheet, Text, View } from "react-native";
import { useRouter } from "expo-router";
import { stateCount, stateName } from "@web/lib/state-labels";
import { loadAll, signOut } from "../../src/runtime/actions";
import { useStore } from "../../src/runtime/store";
import { stateCounts } from "../../src/domain/counts";
import { agentRows, groupAgents, machineName, projectOf } from "../../src/domain/grouping";
import { AgentRow } from "../../src/ui/AgentRow";
import { Button, Muted } from "../../src/ui/parts";
import { m } from "../../src/i18n";
import { font, space, useTheme } from "../../src/theme";

export default function Agents() {
  const p = useTheme();
  const router = useRouter();
  const sessions = useStore((s) => s.sessions);
  const machines = useStore((s) => s.machines);
  const loaded = useStore((s) => s.sessionsLoaded);
  const error = useStore((s) => s.sessionsError);
  const [refreshing, setRefreshing] = useState(false);
  const counts = useMemo(() => stateCounts(agentRows(sessions)), [sessions]);
  const sections = useMemo(() => groupAgents(sessions), [sessions]);
  const refresh = async () => {
    setRefreshing(true);
    await loadAll();
    setRefreshing(false);
  };
  const strip = [
    { n: counts.needs, label: stateName("needs_you"), spoken: stateCount("needs_you", counts.needs), color: p.needs },
    { n: counts.working, label: stateName("working"), spoken: stateCount("working", counts.working), color: p.working },
    { n: counts.unread, label: stateName("unread"), spoken: stateCount("unread", counts.unread), color: p.unread },
  ];
  return (
    <View style={{ flex: 1, backgroundColor: p.page }}>
      <View style={[styles.strip, { backgroundColor: p.surface, borderColor: p.line }]} accessible accessibilityLabel={strip.map((c) => c.spoken).join(", ")}>
        {strip.map((c, i) => (
          <View key={i} style={styles.stat} testID={`count-${i}`} importantForAccessibility="no-hide-descendants">
            <Text style={{ color: c.n ? c.color : p.muted, fontSize: font.title, fontWeight: "700" }}>{c.n}</Text>
            <Text numberOfLines={1} style={{ color: p.ink2, fontSize: font.caption }}>
              {c.label}
            </Text>
          </View>
        ))}
      </View>
      <SectionList
        sections={sections.map((s) => ({ ...s, data: s.rows }))}
        keyExtractor={(s) => `${s.machine_id}/${s.session_id}`}
        refreshControl={<RefreshControl refreshing={refreshing} onRefresh={() => void refresh()} />}
        stickySectionHeadersEnabled={false}
        ListEmptyComponent={
          <View style={styles.empty}>
            <Muted>{error ? m("agents_failed") : loaded ? m("agents_empty") : m("agents_loading")}</Muted>
            {error ? <Button label={m("retry")} tone="quiet" onPress={() => void refresh()} /> : null}
          </View>
        }
        ListFooterComponent={<View style={styles.footer}><Button label={m("sign_out")} tone="quiet" onPress={() => void signOut()} /></View>}
        renderSectionHeader={({ section }) => (
          <Text style={[styles.header, { color: section.needsYou ? p.needs : p.ink2 }]}>{section.needsYou ? m("needs_you_first") : section.title || m("other_projects")}</Text>
        )}
        renderItem={({ item, section }) => (
          <AgentRow
            s={item}
            machine={[section.needsYou ? projectOf(item).name : "", machineName(machines, item.machine_id)].filter(Boolean).join(" · ")}
            onPress={() => router.push(`/session/${encodeURIComponent(item.machine_id)}/${encodeURIComponent(item.session_id)}`)}
          />
        )}
      />
    </View>
  );
}

const styles = StyleSheet.create({
  strip: { flexDirection: "row", paddingVertical: space.md, borderBottomWidth: StyleSheet.hairlineWidth },
  stat: { flex: 1, alignItems: "center", gap: space.xs },
  header: { fontSize: font.small, fontWeight: "700", paddingHorizontal: space.lg, paddingTop: space.xl, paddingBottom: space.sm, textTransform: "none" },
  empty: { padding: space.xl, gap: space.md, alignItems: "center" },
  footer: { padding: space.xl },
});
