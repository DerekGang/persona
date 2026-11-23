// Builds src/i18n/generated/<lang>.json from the web's locale files (read in place) plus the mobile app's own strings.
// Only the namespaces the shared modules read are taken, so the bundle stays small. Run by npm's pre* scripts.
import { readFileSync, readdirSync, mkdirSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const root = join(here, "..");
const locales = join(root, "..", "web", "src", "i18n", "locales");
const WEB_NAMESPACES = [/^lib-/, /^chat-interactions$/, /^approval-question$/];
const LANGS = ["en", "zh-Hans", "ja"];

mkdirSync(join(root, "src", "i18n", "generated"), { recursive: true });
for (const lang of LANGS) {
  const out = {};
  for (const file of readdirSync(join(locales, lang)).sort()) {
    if (!file.endsWith(".json") || file.startsWith("_")) continue;
    const ns = file.replace(/\.json$/, "");
    if (!WEB_NAMESPACES.some((re) => re.test(ns))) continue;
    const entries = JSON.parse(readFileSync(join(locales, lang, file), "utf8"));
    for (const [k, v] of Object.entries(entries)) out[`${ns}.${k}`] = v;
  }
  const own = JSON.parse(readFileSync(join(root, "src", "i18n", `${lang}.json`), "utf8"));
  for (const [k, v] of Object.entries(own)) out[`mobile.${k}`] = v;
  writeFileSync(join(root, "src", "i18n", "generated", `${lang}.json`), JSON.stringify(out));
  console.log(`${lang}: ${Object.keys(out).length} keys`);
}
