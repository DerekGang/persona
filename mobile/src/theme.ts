// DEMO-ONLY mirror of the web's design tokens (web/src/styles.css): colours, radii and a 4 pt spacing grid, light and
// dark. The real app would generate this from one token source shared with the web, iOS and Android. Screens read tokens
// from here and never hard-code colours or sizes.
import { useColorScheme } from "react-native";

export interface Palette {
  page: string;
  surface: string;
  raised: string;
  line: string;
  ink: string;
  ink2: string;
  muted: string;
  accent: string;
  onAccent: string;
  needs: string;
  working: string;
  unread: string;
  idle: string;
  offline: string;
  danger: string;
  /** True for the dark palette. */
  dark: boolean;
}

export const light: Palette = {
  page: "#f6f6f4",
  surface: "#ffffff",
  raised: "#efefec",
  line: "#e2e2dd",
  ink: "#1c1c1a",
  ink2: "#4a4a46",
  muted: "#85857f",
  accent: "#3b5bdb",
  onAccent: "#ffffff",
  needs: "#d9480f",
  working: "#2f9e44",
  unread: "#3b5bdb",
  idle: "#adb5bd",
  offline: "#ced4da",
  danger: "#c92a2a",
  dark: false,
};

export const dark: Palette = {
  page: "#121211",
  surface: "#1c1c1a",
  raised: "#262624",
  line: "#34342f",
  ink: "#f1f1ee",
  ink2: "#c5c5bf",
  muted: "#8f8f88",
  accent: "#748ffc",
  onAccent: "#10131f",
  needs: "#ff8c5a",
  working: "#51cf66",
  unread: "#748ffc",
  idle: "#6c757d",
  offline: "#495057",
  danger: "#ff6b6b",
  dark: true,
};

export const space = { xs: 4, sm: 8, md: 12, lg: 16, xl: 24, xxl: 32 } as const;
export const radius = { sm: 8, md: 12, lg: 18, pill: 999 } as const;
export const font = { caption: 12, small: 14, body: 16, title: 20, large: 28 } as const;
export const size = { tap: 44, dot: 8, composer: 120, asks: 460, tabBar: 64 } as const;

export function useTheme(): Palette {
  return useColorScheme() === "dark" ? dark : light;
}

export function stateTone(p: Palette, state: string): string {
  switch (state) {
    case "needs_you":
      return p.needs;
    case "working":
      return p.working;
    case "unread":
      return p.unread;
    case "offline":
      return p.offline;
    default:
      return p.idle;
  }
}
