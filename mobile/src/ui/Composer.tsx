import { useState } from "react";
import { Pressable, StyleSheet, Text, TextInput, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { m } from "../i18n";
import { font, radius, size, space, useTheme } from "../theme";

/** A multiline box and Send; Send is off while the text is empty. */
export function Composer({ placeholder, onSend }: { placeholder: string; onSend: (text: string) => void }) {
  const p = useTheme();
  const insets = useSafeAreaInsets();
  const [text, setText] = useState("");
  const ready = text.trim().length > 0;
  const send = () => {
    if (!ready) return;
    onSend(text);
    setText("");
  };
  return (
    <View style={[styles.bar, { backgroundColor: p.surface, borderColor: p.line, paddingBottom: space.sm + insets.bottom }]}>
      <TextInput
        value={text}
        onChangeText={setText}
        placeholder={placeholder}
        placeholderTextColor={p.muted}
        multiline
        accessibilityLabel={placeholder}
        style={[styles.input, { color: p.ink, backgroundColor: p.raised }]}
      />
      <Pressable accessibilityRole="button" accessibilityLabel={m("send")} accessibilityState={{ disabled: !ready }} onPress={send} disabled={!ready} style={[styles.send, { backgroundColor: ready ? p.accent : p.raised }]}>
        <Text numberOfLines={1} style={{ color: ready ? p.onAccent : p.muted, fontWeight: "600", fontSize: font.body }}>
          {m("send")}
        </Text>
      </Pressable>
    </View>
  );
}

const styles = StyleSheet.create({
  bar: { flexDirection: "row", alignItems: "flex-end", gap: space.sm, padding: space.sm, borderTopWidth: StyleSheet.hairlineWidth },
  input: { flex: 1, maxHeight: size.composer, minHeight: size.tap, borderRadius: radius.lg, paddingHorizontal: space.md, paddingVertical: space.sm, fontSize: font.body },
  send: { minHeight: size.tap, paddingHorizontal: space.lg, borderRadius: radius.lg, alignItems: "center", justifyContent: "center" },
});
