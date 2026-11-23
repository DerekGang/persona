import type { ReactNode } from "react";
import { Pressable, StyleSheet, Text, View, type ViewStyle } from "react-native";
import { font, radius, size, space, useTheme } from "../theme";

export function Card({ children, style }: { children: ReactNode; style?: ViewStyle }) {
  const p = useTheme();
  return <View style={[styles.card, { backgroundColor: p.surface, borderColor: p.line }, style]}>{children}</View>;
}

export function Button({ label, onPress, tone = "primary", disabled, busy }: { label: string; onPress: () => void; tone?: "primary" | "quiet" | "danger"; disabled?: boolean; busy?: boolean }) {
  const p = useTheme();
  const bg = tone === "primary" ? p.accent : "transparent";
  const fg = tone === "primary" ? p.onAccent : tone === "danger" ? p.danger : p.accent;
  return (
    <Pressable
      accessibilityRole="button"
      onPress={onPress}
      disabled={disabled || busy}
      style={({ pressed }) => [styles.button, { backgroundColor: bg, borderColor: tone === "primary" ? bg : p.line, opacity: disabled || busy ? 0.5 : pressed ? 0.8 : 1 }]}
    >
      <Text numberOfLines={1} style={[styles.buttonText, { color: fg }]}>
        {label}
      </Text>
    </Pressable>
  );
}

/** A coloured dot and a plain state word. */
export function StateChip({ label, color }: { label: string; color: string }) {
  const p = useTheme();
  return (
    <View style={styles.chip}>
      <View style={[styles.dot, { backgroundColor: color }]} />
      <Text numberOfLines={1} style={[styles.chipText, { color: p.ink2 }]}>
        {label}
      </Text>
    </View>
  );
}

export function Muted({ children, style }: { children: ReactNode; style?: object }) {
  const p = useTheme();
  return <Text style={[{ color: p.muted, fontSize: font.small }, style]}>{children}</Text>;
}

const styles = StyleSheet.create({
  card: { borderWidth: StyleSheet.hairlineWidth, borderRadius: radius.md, padding: space.lg, gap: space.md },
  button: { minHeight: size.tap, borderRadius: radius.md, borderWidth: StyleSheet.hairlineWidth, paddingHorizontal: space.lg, alignItems: "center", justifyContent: "center" },
  buttonText: { fontSize: font.body, fontWeight: "600" },
  chip: { flexDirection: "row", alignItems: "center", gap: space.xs },
  dot: { width: size.dot, height: size.dot, borderRadius: radius.pill },
  chipText: { fontSize: font.caption },
});
