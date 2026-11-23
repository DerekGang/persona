// Runs the parity tests under the web's own vitest config (its plugins, catalogs and test setup), with `@web` added, so
// the web's real modules load unshimmed and can be compared with the mobile copies. `npm run test:parity`.
import path from "node:path";
import { mergeConfig } from "vitest/config";
import web from "../../web/vite.config";

export default mergeConfig(web as never, {
  root: path.resolve(import.meta.dirname, "../../web"),
  resolve: { alias: [{ find: /^@web\//, replacement: path.resolve(import.meta.dirname, "../../web/src") + "/" }] },
  server: { fs: { allow: [path.resolve(import.meta.dirname, "../..")] } },
  test: { include: [path.resolve(import.meta.dirname, "*.parity.test.ts")], dir: path.resolve(import.meta.dirname) },
});
