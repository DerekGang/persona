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
async function device(theme, phone = false, { width = phone ? 390 : 1440, large = false } = {}) {
  const ctx = await browser.newContext({
    viewport: { width, height: phone ? 844 : 900 },
    isMobile: phone, hasTouch: phone, colorScheme: theme, locale: "en-US",
    permissions: ["clipboard-read", "clipboard-write"],
  });
  await ctx.route("**/*", (route) => allowed.has(new URL(route.request().url()).origin) ? route.continue() : route.abort());
  await ctx.addInitScript(({ theme, large }) => {
    localStorage.setItem("persona.language", "en");
    localStorage.setItem("persona.theme", theme);
    if (large) localStorage.setItem("persona.text-size", "large");
  }, { theme, large });
  const page = await ctx.newPage();
  const errors = [];
  page.on("pageerror", (error) => errors.push(error.message));
  page.setDefaultTimeout(15000);
  return { page, errors };
}

async function rowCount(page, n) {
  await page.waitForFunction((count) => document.querySelectorAll('ol[aria-label="Starred"] li > article').length === count, n);
}

let cleanup = false;
try {
  assert.deepEqual((await onHub()).stars, [], "use an empty mock");
  cleanup = true;
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
  await shot(mobile.page, "agent-message-phone-dark");

  await desktop.page.locator(`a[href*="${encodeURIComponent(stored[0].item_id)}"]`).click();
  await desktop.page.locator("[data-search-hit]").waitFor();
  assert.equal(new URL(desktop.page.url()).searchParams.get("at"), stored[0].item_id);
  await shot(desktop.page, "assistant-message-light");
  await desktop.page.getByRole("link", { name: /Starred/ }).first().click();

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

  await desktop.page.getByRole("link", { name: /Starred/ }).first().click();
  await rowCount(desktop.page, 3);
  await onHub("POST", "", {
    machine_id: "macbook", session_id: stored[1].session_id, item_id: "long-example",
    conversation: "agent", role: "assistant", title: "Long review snapshot", speaker: "Reviewer",
    text: "## Findings\n\n" + Array.from({ length: 9 }, (_, i) => `- Note ${i + 1} for the documentation review.\n`).join("") +
      "\nA unique note after the folded preview.",
    omitted_chars: 23,
  });
  const largeWide = await device("light", false, { large: true });
  const largeNarrow = await device("dark", true, { width: 320, large: true });
  for (const [name, layout] of [["desktop", largeWide], ["phone-320", largeNarrow]]) {
    await layout.page.goto(web + "/starred?token=mock");
    const long = layout.page.getByRole("article").filter({ hasText: "Long review snapshot" });
    await long.getByText("23 more characters in the original message").waitFor();
    assert.equal(await long.getByText("A unique note after the folded preview.").count(), 0);
    await shot(layout.page, `starred-${name}-large-folded`);
    await long.getByRole("button", { name: "Show saved text" }).click();
    await long.getByText("A unique note after the folded preview.").waitFor();
    assert.equal(await long.evaluate((card) => [...card.querySelectorAll("button, a")].every((action) => {
      const box = action.getBoundingClientRect();
      return box.left >= 0 && box.right <= innerWidth && box.height > 0;
    })), true, "all actions fit at the largest supported text size");
    await shot(layout.page, `starred-${name}-large-expanded`);
  }

  const old = await device("light", true, { width: 320, large: true });
  await old.page.route("**/api/v1/assistant/starred**", (route) => route.fulfill({
    status: 404, contentType: "application/json", body: '{"error":"Not found"}',
  }));
  await old.page.goto(web + "/starred?token=mock");
  await old.page.getByRole("heading", { name: "Starred messages are unavailable" }).waitFor();
  assert.equal(await old.page.getByRole("link", { name: "Starred", exact: true }).count(), 0);
  await shot(old.page, "starred-older-assistant-320-large");

  assert.deepEqual(desktop.errors.concat(mobile.errors, largeWide.errors, largeNarrow.errors, old.errors), []);
  console.log("ok: both message menus and exact links, two-device push, filter, copy, undo, orphaned snapshot, older assistant, and largest text at 320 px");
} finally {
  await browser.close();
  if (cleanup) {
    for (const star of (await onHub()).stars) await onHub("DELETE", "/" + encodeURIComponent(star.id));
  }
}
