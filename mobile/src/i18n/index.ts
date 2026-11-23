// The mobile app's strings (mobile/src/i18n/*.json, keys read as "mobile.<key>") and the web's catalog behind one t().
import { t, type Vars } from "../shims/i18n";

export { t, currentLanguage } from "../shims/i18n";
/** A string of the mobile app itself (src/i18n/<lang>.json). */
export const m = (key: string, vars?: Vars): string => t(`mobile.${key}`, vars);
