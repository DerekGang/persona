// Run with MOCK_STARRED=1 PORT=18797 python3 scripts/mock/mock_hub.py and
// PERSONA_HUB_URL=http://127.0.0.1:18797 vite --host 127.0.0.1 --port 5797.
// node web/e2e/starred-messages.mjs [--shots tmp/starred-shots]
import assert from "node:assert/strict";
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { chromium } from "playwright-core";

const web = process.env.STARRED_WEB ?? "http://127.0.0.1:5797";
const hub = process.env.STARRED_HUB ?? "http://127.0.0.1:18797";
const allowed = new Set([web, hub].map((url) => {
  const parsed = new URL(url);
  assert.equal(parsed.hostname, "127.0.0.1", "only a loopback mock is allowed");
  return parsed.origin;
}));
const shotsArg = process.argv.indexOf("--shots");
const shots = shotsArg < 0 ? undefined : process.argv[shotsArg + 1];
if (shots) mkdirSync(shots, { recursive: true });
const shot = (page, name) => shots && page.screenshot({ path: join(shots, name + ".png"), animations: "disabled" });

const review = "/s/macbook/a1f3c2d4-0000-4000-8000-000000000006?view=chat";
const path = "/api/v1/assistant/starred";
const onHub = async (method = "GET", suffix = "", json) => {
  const response = await fetch(hub + path + suffix, {
    method,
    headers: { Authorization: "Bearer mock", "Content-Type": "application/json" },
    ...(json && { body: JSON.stringify(json) }),
    signal: AbortSignal.timeout(8000),
  });
  assert.ok(response.ok, `${method} ${path} returned ${response.status}`);
  return await response.json();
};

const browser = await chromium.launch({ headless: true, ...(process.env.STARRED_BROWSER ? { executablePath: process.env.STARRED_BROWSER } : { channel: "chrome" }) });
async function device(theme, phone = false) {
  const ctx = await browser.newContext({
    viewport: phone ? { width: 390, height: 844 } : { width: 1440, height: 900 },
    isMobile: phone, hasTouch: phone, colorScheme: theme, locale: "en-US",
    permissions: ["clipboard-read", "clipboard-write"],
  });
  await ctx.route("**/*", (route) => allowed.has(new URL(route.request().url()).origin) ? route.continue() : route.abort());
  await ctx.addInitScript((theme) => {
    localStorage.setItem("persona.language", "en");
    localStorage.setItem("persona.theme", theme);
  }, theme);
  const page = await ctx.newPage();
  const errors = [];
  page.on("pageerror", (error) => errors.push(error.message));
  page.setDefaultTimeout(15000);
  return { page, errors };
}

async function rowCount(page, n) {
  await page.waitForFunction((count) => document.querySelectorAll('ol[aria-label="Starred"] li > article').length === count, n);
}

try {
  assert.deepEqual((await onHub()).stars, [], "use an empty mock");
  const desktop = await device("light");
  await desktop.page.goto(web + review + "&token=mock");
  const desktopUser = desktop.page.locator("[data-user-bubble]").filter({ hasText: "PR #42" }).first();
  await desktopUser.waitFor();
  await desktop.page.getByRole("link", { name: "Starred", exact: true }).first().waitFor();
  await desktopUser.getByText("PR #42", { exact: false }).first().click({ button: "right" });
  await desktop.page.getByRole("menuitem", { name: "Star", exact: true }).click();
  await desktopUser.locator('[data-star-mark="saved"]').waitFor();
  await shot(desktop.page, "agent-message-light");

  const mobile = await device("dark", true);
  await mobile.page.goto(web + "/starred?token=mock");
  await mobile.page.getByRole("heading", { name: "Starred", exact: true }).waitFor();
  await rowCount(mobile.page, 1);
  // The mobile list is open while a separate device stars an Assistant reply.
  await desktop.page.goto(web + "/");
  const reply = desktop.page.locator("[data-answer=true]").last();
  await reply.waitFor();
  await reply.locator("p").first().click({ button: "right" });
  await desktop.page.getByRole("menuitem", { name: "Star", exact: true }).click();
  await rowCount(mobile.page, 2);
  const stored = (await onHub()).stars;
  assert.deepEqual(stored.map((star) => [star.conversation, star.role]), [["assistant", "assistant"], ["agent", "user"]]);

  await desktop.page.getByRole("link", { name: /Starred/ }).first().click();
  await rowCount(desktop.page, 2);
  await shot(desktop.page, "starred-desktop-light");
  await shot(mobile.page, "starred-phone-dark");
  const search = mobile.page.getByRole("textbox", { name: "Search starred messages" });
  await search.fill("PR #42");
  await rowCount(mobile.page, 1);
  const card = mobile.page.getByRole("article");
  await card.getByRole("button", { name: "Copy", exact: true }).click();
  assert.match(await mobile.page.evaluate(() => navigator.clipboard.readText()), /PR #42/);
  await card.getByRole("button", { name: "Unstar" }).click();
  await rowCount(desktop.page, 1);
  await mobile.page.getByRole("button", { name: "Undo" }).click();
  await rowCount(desktop.page, 2);

  await card.getByRole("link", { name: "Open original" }).click();
  await mobile.page.waitForFunction(() => new URLSearchParams(location.search).has("at"));
  await mobile.page.locator("[data-search-hit]").waitFor();
  const source = await onHub("POST", "", {
    machine_id: "macbook", session_id: stored[1].session_id, item_id: "removed-example",
    conversation: "agent", role: "assistant", title: "Former conversation", speaker: "Agent",
    text: "Saved before the original went away.",
  });
  await desktop.page.getByText("Saved before the original went away.").waitFor();
  await desktop.page.reload();
  await desktop.page.getByText("Saved before the original went away.").waitFor();
  const orphan = desktop.page.getByRole("article").filter({ hasText: "Former conversation" });
  await orphan.getByRole("link", { name: "Open original" }).click();
  assert.equal(new URL(desktop.page.url()).searchParams.get("at"), source.star.item_id);

  assert.deepEqual(desktop.errors.concat(mobile.errors), []);
  console.log("ok: Agent and Assistant menus, persisted markers, two-device push, filter, copy, remove, undo, deep link, and source-independent snapshot");
} finally {
  await browser.close();
}
