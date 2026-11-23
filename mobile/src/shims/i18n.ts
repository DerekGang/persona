// Stands in for web/src/i18n/index.ts (the web's loader uses import.meta.glob and the DOM). Same call surface for the
// pure modules that say t(...) and format dates; the catalogs are the web's own locale files, built by
// scripts/build-catalog.mjs. The language is fixed at launch from the device, as the web's is per page load.
import * as Localization from "expo-localization";
import { Platform } from "react-native";
import en from "../i18n/generated/en.json";
import ja from "../i18n/generated/ja.json";
import zhHans from "../i18n/generated/zh-Hans.json";

export type Lang = "en" | "zh-Hans" | "ja";
export const LANGS: readonly Lang[] = ["zh-Hans", "en", "ja"];
export const SOURCE_LANG: Lang = "zh-Hans";
export const FALLBACK_LANG: Lang = "en";
export type Vars = Record<string, string | number>;

const CATALOGS: Record<Lang, Record<string, string>> = { en, "zh-Hans": zhHans, ja };

/** docs/protocol/i18n.md: the language subtag picks the language (any Chinese is zh-Hans); anything else is English. */
export function resolveLanguage(tag: string | null | undefined): Lang {
  const primary = (tag ?? "").trim().replace(/_/g, "-").split("-")[0]!.toLowerCase();
  return primary === "zh" ? "zh-Hans" : primary === "ja" ? "ja" : "en";
}

function deviceTag(): string {
  try {
    const first = Localization.getLocales()[0]?.languageTag;
    if (first) return first;
  } catch {
    // fall through
  }
  if (Platform.OS === "web" && typeof navigator !== "undefined") return navigator.languages?.[0] ?? navigator.language ?? "en";
  return "en";
}

let lang: Lang = resolveLanguage(deviceTag());
let sys = deviceTag();

/** Tests choose a language; the app never switches at runtime. */
export function setLanguage(l: Lang) {
  lang = l;
  sys = l;
}
export const currentLanguage = (): Lang => lang;
export const formatLocale = (): string => (resolveLanguage(sys) === lang ? sys.replace(/_/g, "-") : lang);

function lookup(l: Lang, key: string, vars?: Vars): string | undefined {
  const c = CATALOGS[l];
  if (vars && typeof vars.count === "number") {
    const text = c[`${key}_${new Intl.PluralRules(l).select(vars.count)}`] ?? c[`${key}_other`];
    if (text !== undefined) return text;
  }
  return c[key];
}

export function interpolate(text: string, vars?: Vars): string {
  if (!vars) return text;
  return text.replace(/\{(\w+)\}/g, (m, name: string) => (name in vars ? String(vars[name]) : m));
}

export function t(key: string, vars?: Vars): string {
  const text = lookup(lang, key, vars) ?? lookup(FALLBACK_LANG, key, vars) ?? lookup(SOURCE_LANG, key, vars);
  return text === undefined ? key : interpolate(text, vars);
}

export function hasKey(key: string): boolean {
  return [lang, FALLBACK_LANG, SOURCE_LANG].some((l) => key in CATALOGS[l] || `${key}_other` in CATALOGS[l]);
}

export function formatNumber(n: number, opts?: Intl.NumberFormatOptions): string {
  return new Intl.NumberFormat(formatLocale(), opts).format(n);
}
export function formatDate(d: Date | number | string, opts: Intl.DateTimeFormatOptions = { dateStyle: "medium" }): string {
  const date = d instanceof Date ? d : new Date(d);
  return Number.isNaN(date.getTime()) ? "" : new Intl.DateTimeFormat(formatLocale(), opts).format(date);
}
export function formatRelative(value: number, unit: Intl.RelativeTimeFormatUnit, opts: Intl.RelativeTimeFormatOptions = { numeric: "auto" }): string {
  return new Intl.RelativeTimeFormat(formatLocale(), opts).format(value, unit);
}
export function formatList(items: string[], opts: Intl.ListFormatOptions = { type: "conjunction" }): string {
  return new Intl.ListFormat(formatLocale(), opts).format(items);
}
