import { useState } from "react";
import { KeyboardAvoidingView, Platform, ScrollView, StyleSheet, Text, TextInput, View } from "react-native";
import { ApiError } from "../src/runtime/client";
import { connect } from "../src/runtime/actions";
import { Button } from "../src/ui/parts";
import { m } from "../src/i18n";
import { font, radius, size, space, useTheme } from "../src/theme";

const DEFAULT_HUB = process.env.EXPO_PUBLIC_HUB_URL ?? (Platform.OS === "web" && typeof location !== "undefined" ? location.origin : "");

/** What went wrong, in words a person can act on. */
export function connectError(e: unknown): string {
  if (e instanceof ApiError) return e.status === 401 || e.status === 403 ? m("err_token") : m("err_unreachable");
  if (e instanceof TypeError && /cleartext|secure|ssl|tls/i.test(e.message)) return m("err_tls");
  return m("err_unreachable");
}

export default function Connect() {
  const p = useTheme();
  const [base, setBase] = useState(DEFAULT_HUB);
  const [token, setToken] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const go = async () => {
    if (!base.trim() || !token.trim()) return setError(m("err_fields"));
    setBusy(true);
    setError("");
    try {
      await connect(base, token, `Persona demo (${Platform.OS})`);
    } catch (e) {
      setError(connectError(e));
    } finally {
      setBusy(false);
    }
  };
  const input = [styles.input, { color: p.ink, backgroundColor: p.surface, borderColor: p.line }];
  return (
    <KeyboardAvoidingView style={{ flex: 1, backgroundColor: p.page }} behavior={Platform.OS === "ios" ? "padding" : undefined}>
      <ScrollView contentContainerStyle={styles.page} keyboardShouldPersistTaps="handled">
        <Text style={[styles.title, { color: p.ink }]}>{m("connect_title")}</Text>
        <Text style={{ color: p.ink2, fontSize: font.body }}>{m("connect_intro")}</Text>
        <View style={styles.field}>
          <Text style={{ color: p.ink2, fontSize: font.small }}>{m("hub_address")}</Text>
          <TextInput value={base} onChangeText={setBase} autoCapitalize="none" autoCorrect={false} keyboardType="url" placeholder="https://hub.example.com" placeholderTextColor={p.muted} accessibilityLabel={m("hub_address")} style={input} />
        </View>
        <View style={styles.field}>
          <Text style={{ color: p.ink2, fontSize: font.small }}>{m("access_token")}</Text>
          <TextInput value={token} onChangeText={setToken} autoCapitalize="none" autoCorrect={false} secureTextEntry placeholder={m("access_token_hint")} placeholderTextColor={p.muted} accessibilityLabel={m("access_token")} style={input} />
        </View>
        {error ? (
          <Text accessibilityRole="alert" style={{ color: p.danger, fontSize: font.small }}>
            {error}
          </Text>
        ) : null}
        <Button label={busy ? m("connecting") : m("connect")} busy={busy} onPress={() => void go()} />
      </ScrollView>
    </KeyboardAvoidingView>
  );
}

const styles = StyleSheet.create({
  page: { padding: space.xl, gap: space.lg, paddingTop: space.xxl * 2 },
  title: { fontSize: font.large, fontWeight: "700" },
  field: { gap: space.xs },
  input: { minHeight: size.tap, borderRadius: radius.md, borderWidth: StyleSheet.hairlineWidth, paddingHorizontal: space.md, fontSize: font.body },
});
