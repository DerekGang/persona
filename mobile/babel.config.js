// The web's shared modules read import.meta (Vite): let babel-preset-expo rewrite it for Metro.
module.exports = function (api) {
  api.cache(true);
  return { presets: [["babel-preset-expo", { unstable_transformImportMeta: true }]] };
};
