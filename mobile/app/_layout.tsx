import { useEffect } from "react";
import { ActivityIndicator, View } from "react-native";
import { Redirect, Stack, useSegments } from "expo-router";
import { StatusBar } from "expo-status-bar";
import { boot } from "../src/runtime/actions";
import { useStore } from "../src/runtime/store";
import { m } from "../src/i18n";
import { useTheme } from "../src/theme";

export default function Root() {
  const p = useTheme();
  const auth = useStore((s) => s.auth);
  const segments = useSegments();
  useEffect(() => {
    void boot();
  }, []);
  const onConnect = (segments as string[])[0] === "connect";
  if (auth === "loading") {
    return (
      <View style={{ flex: 1, alignItems: "center", justifyContent: "center", backgroundColor: p.page }}>
        <ActivityIndicator color={p.accent} />
      </View>
    );
  }
  if (auth === "signedOut" && !onConnect) return <Redirect href="/connect" />;
  if (auth === "ready" && onConnect) return <Redirect href="/" />;
  return (
    <>
      <StatusBar style={p.dark ? "light" : "dark"} />
      <Stack screenOptions={{ headerStyle: { backgroundColor: p.surface }, headerTintColor: p.ink, contentStyle: { backgroundColor: p.page } }}>
        <Stack.Screen name="(tabs)" options={{ headerShown: false }} />
        <Stack.Screen name="connect" options={{ title: m("connect_title"), headerShown: false }} />
        <Stack.Screen name="session/[machine]/[sid]" options={{ title: m("session_title") }} />
      </Stack>
    </>
  );
}
