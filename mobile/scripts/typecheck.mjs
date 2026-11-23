// Type-checks the mobile app. tsc also walks the web files that the mobile app imports in place, and the web has its own
// config (Vite types, DOM): their diagnostics belong to `cd web && npm run typecheck`. This fails on any diagnostic in
// mobile/ and reports how many it left to the web's own check.
import { spawnSync } from "node:child_process";

const r = spawnSync("npx", ["tsc", "--noEmit", "--pretty", "false"], { encoding: "utf8" });
const lines = (r.stdout + r.stderr).split("\n");
const diag = /^(\S.*?)\((\d+),(\d+)\): error/;
let mine = 0;
let web = 0;
const out = [];
for (const line of lines) {
  const m = diag.exec(line);
  if (m) {
    if (m[1].startsWith("../web/")) web++;
    else {
      mine++;
      out.push(line);
    }
  } else if (mine && line.startsWith(" ")) out.push(line);
}
console.log(out.join("\n"));
console.log(`typecheck: ${mine} error(s) in mobile/, ${web} in imported web files (checked by web's own tsc)`);
process.exit(mine ? 1 : 0);
