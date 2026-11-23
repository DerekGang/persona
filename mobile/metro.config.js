// Demo only: the mobile app imports the web client's pure TypeScript (web/src/lib) in place, without forking it.
// - `@web/...` points at ../web/src.
// - Web modules resolve packages from mobile/node_modules only, so there is one React.
// - "Seam" modules (i18n loader, hub api, base path, manager choice) are replaced by files in src/shims.
// - Anything in web/src that is DOM UI (a .tsx file, or ui/ and views/) is a build error on purpose.
const path = require("path");
const { getDefaultConfig } = require("expo/metro-config");

const config = getDefaultConfig(__dirname);
const webSrc = path.resolve(__dirname, "../web/src");
const shims = path.resolve(__dirname, "src/shims");

config.watchFolders = [...(config.watchFolders ?? []), webSrc];
config.resolver.nodeModulesPaths = [path.resolve(__dirname, "node_modules")];
config.resolver.disableHierarchicalLookup = true;

// web file (relative to web/src, no extension) -> the shim that stands in for it
const SEAMS = {
  "i18n/index": "i18n.ts",
  "lib/api": "api.ts",
  "lib/base": "base.ts",
  "lib/manager-choice": "manager-choice.ts",
};

const upstream = config.resolver.resolveRequest;
config.resolver.resolveRequest = (context, moduleName, platform) => {
  const resolve = (name) => (upstream ?? context.resolveRequest)(context, name, platform);
  if (moduleName.startsWith("@web/")) moduleName = path.join(webSrc, moduleName.slice(5));
  const res = resolve(moduleName);
  if (res.type !== "sourceFile") return res;
  if (res.filePath.startsWith(webSrc + path.sep)) {
    const rel = path.relative(webSrc, res.filePath).replace(/\.(tsx?|jsx?)$/, "");
    if (SEAMS[rel]) return { type: "sourceFile", filePath: path.join(shims, SEAMS[rel]) };
    if (/\.tsx$/.test(res.filePath) || /^(ui|views)\//.test(rel)) {
      throw new Error(`web/src/${rel} is DOM UI and cannot be shared with the mobile app (imported from ${context.originModulePath})`);
    }
  }
  return res;
};

module.exports = config;
