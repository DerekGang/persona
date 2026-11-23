import { Tabs } from "expo-router";
import { Ionicons } from "@expo/vector-icons";
import { useMemo } from "react";
import { useStore } from "../../src/runtime/store";
import { agentRows } from "../../src/domain/grouping";
import { stateCounts } from "../../src/domain/counts";
import { m } from "../../src/i18n";
import { size, space, useTheme } from "../../src/theme";

export default function TabsLayout() {
  const p = useTheme();
  const sessions = useStore((s) => s.sessions);
  const needs = useMemo(() => stateCounts(agentRows(sessions)).badge, [sessions]);
  return (
    <Tabs
      screenOptions={{
        headerStyle: { backgroundColor: p.surface },
        headerTintColor: p.ink,
        tabBarStyle: { backgroundColor: p.surface, borderTopColor: p.line, height: size.tabBar, paddingBottom: space.sm },
        tabBarActiveTintColor: p.accent,
        tabBarInactiveTintColor: p.muted,
        sceneStyle: { backgroundColor: p.page },
      }}
    >
      <Tabs.Screen name="index" options={{ title: m("tab_chat"), headerTitle: m("chat_title"), tabBarIcon: ({ color, size }) => <Ionicons name="chatbubble-outline" size={size} color={color as string} /> }} />
      <Tabs.Screen
        name="agents"
        options={{
          title: m("tab_agents"),
          headerTitle: m("agents_title"),
          tabBarBadge: needs > 0 ? needs : undefined,
          tabBarBadgeStyle: { backgroundColor: p.needs, color: p.onAccent },
          tabBarIcon: ({ color, size }) => <Ionicons name="layers-outline" size={size} color={color as string} />,
        }}
      />
    </Tabs>
  );
}
