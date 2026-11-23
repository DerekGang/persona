import path from "node:path";
import { defineConfig, type Plugin } from "vitest/config";

const webSrc = path.resolve(import.meta.dirname, "../web/src");
const shims = path.resolve(import.meta.dirname, "src/shims");
// The same redirects as metro.config.js: web modules that touch the DOM or the web runtime get the demo's shims.
const SEAMS: Record<string, string> = {
  "i18n/index": "i18n.ts",
  "lib/api": "api.ts",
  "lib/base": "base.ts",
  "lib/manager-choice": "manager-choice.ts",
};

const seams = (): Plugin => ({
  name: "persona-mobile-seams",
  enforce: "pre",
  async resolveId(source, importer, options) {
    if (!importer?.startsWith(webSrc + path.sep) || !source.startsWith(".")) return null;
    const res = await this.resolve(source, importer, { ...options, skipSelf: true });
    if (!res || !res.id.startsWith(webSrc + path.sep)) return null;
    const rel = path.relative(webSrc, res.id).replace(/\.(tsx?|jsx?)$/, "");
    return SEAMS[rel] ? path.join(shims, SEAMS[rel]) : null;
  },
});

export default defineConfig({
  plugins: [seams()],
  resolve: {
    alias: [
      { find: /^@web\//, replacement: `${webSrc}/` },
      { find: /^react-native$/, replacement: path.resolve(import.meta.dirname, "test/stubs/react-native.ts") },
      { find: /^expo-localization$/, replacement: path.resolve(import.meta.dirname, "test/stubs/expo-localization.ts") },
      { find: /^expo-secure-store$/, replacement: path.resolve(import.meta.dirname, "test/stubs/expo-secure-store.ts") },
    ],
  },
  server: { fs: { allow: [path.resolve(import.meta.dirname, "..")] } },
  test: { environment: "node", include: ["test/**/*.test.ts"], server: { deps: { inline: [/web\/src/] } } },
});
