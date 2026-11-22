// Agents → Bots navigation and the existing personal-Agent conversation, against a local mock hub and synthetic bot responses.
// Start mock_hub.py with MOCK_SESSIONS_LIST=1 and Vite with PERSONA_HUB_URL pointing at it, then:
// node web/e2e/agents-bots.mjs <web URL> --hub <mock URL> --shots <directory> [--executable <chromium>]
import assert from "node:assert/strict";
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { chromium } from "playwright-core";

const args = process.argv.slice(2);
const flag = (name) => args[args.indexOf(name) + 1];
const base = (args[0] ?? "").replace(/\/$/, "");
const hub = flag("--hub")?.replace(/\/$/, "");
const shots = flag("--shots");
const executable = flag("--executable") || process.env.PERSONA_E2E_CHROMIUM;
if (!base || !hub || !shots) throw new Error("usage: agents-bots.mjs <web URL> --hub <mock URL> --shots <directory> [--executable <chromium>]");
mkdirSync(shots, { recursive: true });
const browser = await chromium.launch(executable ? { executablePath: executable, headless: true, args: ["--no-sandbox"] } : { channel: "chrome", headless: true, args: ["--no-sandbox"] });
const watchdog = setTimeout(() => { console.error("FAIL agents-bots browser harness: timeout"); process.exit(2); }, 180_000);
let activePage;

const bot = { id: "bot_research:endpoint", source: "bot_research", sessionKey: "endpoint", accountId: "research", provider: "bot", title: "Research bot", kind: "bot", canSend: true, unreadCount: 0, rankTimeMs: 1 };
const feishuBot = () => ({ id: "bot_external_task:endpoint", source: "bot_external_task", sessionKey: "endpoint", accountId: "external_task", provider: "feishu_bot", title: "Synthetic collaborator", kind: "bot", canSend: true, unreadCount: 0, rankTimeMs: 1, actions: ["complete", "abandon"], proactive: "unsupported" });
const textBlock = (text) => [{ type: "text", text }];

try {
  for (const fixture of [
    { name: "wide-light", theme: "light", width: 1440, height: 1000 },
    { name: "wide-dark", theme: "dark", width: 1440, height: 1000 },
    { name: "narrow-light", theme: "light", width: 390, height: 844 },
  ]) {
    const context = await browser.newContext({ viewport: { width: fixture.width, height: fixture.height }, colorScheme: fixture.theme, reducedMotion: "reduce", locale: "en-US" });
    const page = await context.newPage();
    activePage = page;
    const errors = [];
    page.on("pageerror", (error) => errors.push(String(error)));
    const sends = [];
    const replies = [];
    const feishuSends = [];
    const feishuReplies = [];
    const feishuActions = [];
    let feishuSession = feishuBot();
    await page.addInitScript((theme) => {
      localStorage.setItem("persona.language", "en");
      localStorage.setItem("persona.theme", theme);
    }, fixture.theme);
    await page.route("**/api/v1/agents", (route) => route.fulfill({ json: { sources: [
      { source: "bot_research", enabled: true, providers: [{ provider: "bot", available: true }], accounts: [{ accountId: "research", provider: "bot", displayName: "Research bot", sessionCount: 1 }] },
      { source: "bot_external_task", enabled: true, providers: [{ provider: "feishu_bot", available: true }], accounts: [{ accountId: "external_task", provider: "feishu_bot", displayName: "Synthetic collaborator", sessionCount: 1 }] },
    ] } }));
    const listBots = (route) => route.fulfill({ json: { sessions: [bot, feishuSession], total: 2, has_more: false, revision: "1", remove: [] } });
    await page.route("**/api/v1/agents/sessions", listBots);
    await page.route("**/api/v1/agents/sessions?*", listBots);
    await page.route("**/api/v1/agents/prefs", (route) => route.fulfill({ json: { pins: [], view: {}, sections: [], assign: {}, hidden: {} } }));
    await page.route("**/api/v1/agents/hidden", (route) => route.fulfill({ json: { sessions: [] } }));
    await page.route("**/api/v1/agents/outbox*", (route) => route.fulfill({ json: { outbox: [] } }));
    await page.route("**/api/v1/agents/sessions/**", async (route) => {
      const request = route.request();
      const url = new URL(request.url());
      const path = decodeURIComponent(url.pathname);
      if (path.includes("bot_external_task") && path.endsWith("/action")) {
        const input = request.postDataJSON();
        feishuActions.push(input.action);
        feishuSession = { ...feishuSession, canSend: false, actions: [], reviewState: undefined, lifecycle: input.action === "complete" ? "completed" : "abandoned" };
        return route.fulfill({ json: { session: feishuSession } });
      }
      if (path.includes("bot_external_task") && path.endsWith("/messages")) {
        if (request.method() === "POST") {
          const input = request.postDataJSON();
          assert.equal(path, "/api/v1/agents/sessions/bot_external_task:endpoint/messages");
          feishuSends.push(input);
          feishuReplies.push(false);
          return route.fulfill({ json: { duplicate: false, outbox: { clientId: input.clientId, sessionId: feishuSession.id, status: "sent" }, upstream: { accepted: true } } });
        }
        const messages = feishuSends.flatMap((send, i) => {
          const rows = [{ externalId: `lp_public_request_${i + 1}`, sessionKey: "endpoint", role: "user", createTimeMs: Date.now(), position: i * 2 + 1, blocks: textBlock(send.text) }];
          if (feishuReplies[i]) rows.push({ externalId: `lp_public_reply_${i + 1}`, sessionKey: "endpoint", role: "assistant", createTimeMs: Date.now() + 1, position: i * 2 + 2, blocks: textBlock("Exact Feishu reply") });
          return rows;
        });
        return route.fulfill({ json: { session: feishuSession, messages, hasMore: false } });
      }
      if (url.pathname.endsWith("/messages")) {
        if (request.method() === "POST") {
          const input = request.postDataJSON();
          assert.equal(path, "/api/v1/agents/sessions/bot_research:endpoint/messages");
          sends.push(input);
          replies.push(false);
          return route.fulfill({ json: { duplicate: false, outbox: { clientId: input.clientId, sessionId: bot.id, status: "sent" }, upstream: { accepted: true } } });
        }
        const messages = sends.flatMap((send, i) => {
          const rows = [{ externalId: `user-${i + 1}`, sessionKey: "endpoint", role: "user", createTimeMs: Date.now(), position: i * 2 + 1, blocks: textBlock(send.text) }];
          if (replies[i]) rows.push({ externalId: `assistant-${i + 1}`, sessionKey: "endpoint", role: "assistant", createTimeMs: Date.now() + 1, position: i * 2 + 2, blocks: textBlock(i === 0 ? "First bot reply" : "Follow-up bot reply") });
          return rows;
        });
        return route.fulfill({ json: { session: bot, messages, hasMore: false } });
      }
      if (url.pathname.endsWith("/read")) return route.fulfill({ json: { ok: true } });
      return route.fulfill({ json: { session: path.includes("bot_external_task") ? feishuSession : bot } });
    });

    await page.goto(`${base}/agents?token=mock`, { waitUntil: "domcontentloaded", timeout: 30_000 });
    const botsButton = page.locator("header").getByRole("button", { name: "Bots", exact: true });
    await botsButton.waitFor({ state: "visible", timeout: 30_000 });
    assert.equal(await botsButton.count(), 1, "Agents has one direct Bots destination");
    await page.getByRole("main").getByText("Loading…", { exact: true }).waitFor({ state: "hidden", timeout: 15_000 });
    await page.screenshot({ path: join(shots, `agents-bots-navigation-${fixture.name}.png`), animations: "disabled" });
    await botsButton.click();
    await page.waitForFunction(() => location.pathname === "/a", null, { timeout: 15_000 });
    await page.getByRole("link", { name: "Research bot", exact: true }).last().click();
    await page.waitForFunction(() => location.pathname.startsWith("/a/bot_research"), null, { timeout: 15_000 });
    const composer = page.locator("textarea");
    await composer.waitFor({ state: "visible", timeout: 15_000 });
    await composer.fill("First assignment");
    await page.getByRole("button", { name: "Send", exact: true }).click();
    await page.getByText("First assignment", { exact: true }).last().waitFor({ timeout: 15_000 });
    assert.equal(await page.getByText("First bot reply", { exact: true }).count(), 0, "accepted send is not bot completion");
    replies[0] = true;
    await page.evaluate(async (id) => {
      const agents = await import("/src/lib/agents.ts");
      agents.agentsChanged([id]);
    }, bot.id);
    await page.getByText("First bot reply", { exact: true }).waitFor({ timeout: 15_000 });
    assert.equal(await page.getByText("First bot reply", { exact: true }).count(), 1);

    await composer.fill("Follow-up assignment");
    await page.getByRole("button", { name: "Send", exact: true }).click();
    await page.getByText("Follow-up assignment", { exact: true }).last().waitFor({ timeout: 15_000 });
    assert.equal(await page.getByText("Follow-up bot reply", { exact: true }).count(), 0);
    replies[1] = true;
    await page.evaluate(async (id) => {
      const agents = await import("/src/lib/agents.ts");
      agents.agentsChanged([id]);
    }, bot.id);
    await page.getByText("Follow-up bot reply", { exact: true }).waitFor({ timeout: 15_000 });
    assert.equal(await page.getByText("First bot reply", { exact: true }).count(), 1);
    assert.equal(await page.getByText("Follow-up bot reply", { exact: true }).count(), 1);
    assert.deepEqual(sends.map((send) => send.text), ["First assignment", "Follow-up assignment"]);
    assert.notEqual(sends[0].clientId, sends[1].clientId);
    assert.equal(await page.locator("body").textContent().then((text) => text.includes("claude_registered_b")), false, "structured-session identity stays private");
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1), true, "no horizontal overflow");
    await page.screenshot({ path: join(shots, `agents-bots-${fixture.name}.png`), animations: "disabled" });
    assert.deepEqual(errors, [], "no browser page errors");
    console.log(`PASS ${fixture.name}: Agents → Bots → first assignment → reply → follow-up → reply`);

    await page.goto(`${base}/a?token=mock`, { waitUntil: "domcontentloaded", timeout: 30_000 });
    await page.locator("a[href='/a/bot_external_task%3Aendpoint']:visible").last().click();
    await page.waitForFunction(() => location.pathname.startsWith("/a/bot_external_task"), null, { timeout: 15_000 });
    const feishuComposer = page.locator("textarea");
    await feishuComposer.waitFor({ state: "visible", timeout: 15_000 });
    await page.getByText("Proactive messages aren't ingested for this bot.", { exact: true }).waitFor();
    await feishuComposer.fill("First Feishu assignment");
    await page.getByRole("button", { name: "Send", exact: true }).click();
    await page.getByText("First Feishu assignment", { exact: true }).last().waitFor();
    assert.equal(await page.getByText("Completed", { exact: true }).count(), 0, "accepted Feishu delivery is not completion");
    assert.deepEqual(feishuActions, []);
    feishuReplies[0] = true;
    feishuSession = { ...feishuSession, reviewState: "pending_review" };
    await page.evaluate(async (id) => {
      const agents = await import("/src/lib/agents.ts");
      agents.agentsChanged([id]);
    }, feishuSession.id);
    await page.getByText("Exact Feishu reply", { exact: true }).waitFor();
    await page.getByText("Reply is pending review. It isn't owner approval or an Agent instruction.", { exact: true }).waitFor();
    await feishuComposer.fill("Same-task Feishu follow-up");
    await page.getByRole("button", { name: "Send", exact: true }).click();
    await page.getByText("Same-task Feishu follow-up", { exact: true }).last().waitFor();
    assert.equal(feishuSends.length, 2);
    assert.notEqual(feishuSends[0].clientId, feishuSends[1].clientId);
    await page.screenshot({ path: join(shots, `feishu-bots-pending-${fixture.name}.png`), animations: "disabled" });
    await page.getByRole("button", { name: "Task actions", exact: true }).click();
    await page.getByRole("menuitem", { name: "Complete task…", exact: true }).click();
    await page.locator("[role='menuitem']").filter({ hasText: "This closes the external task" }).click();
    await page.getByText("Completed", { exact: true }).waitFor();
    assert.deepEqual(feishuActions, ["complete"]);
    assert.equal(await feishuComposer.isDisabled(), true);
    const feishuText = await page.locator("body").textContent();
    for (const privateID of ["oc_scoped", "omt_scoped", "om_request", "ou_external", "cli_synthetic", "todo_lark_"]) {
      assert.equal(feishuText.includes(privateID), false, `private Feishu identity stays out of the page: ${privateID}`);
    }
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1), true, "Feishu conversation has no horizontal overflow");
    await page.screenshot({ path: join(shots, `feishu-bots-${fixture.name}.png`), animations: "disabled" });
    assert.deepEqual(errors, [], "no browser page errors in the Feishu flow");
    console.log(`PASS ${fixture.name}: Feishu assignment → exact reply → pending review → follow-up → completion`);
    await context.close();
  }
  console.log("PASS agents-bots browser verification (synthetic Orca/Feishu data; wide/narrow and light/dark)");
} catch (error) {
  console.error("FAIL agents-bots browser verification:", error);
  if (activePage) {
    console.error("Active URL:", activePage.url());
    console.error("Page text:", (await activePage.locator("body").textContent().catch(() => "unavailable"))?.slice(0, 4000));
    await activePage.screenshot({ path: join(shots, "agents-bots-failure.png"), animations: "disabled" }).catch(() => {});
  }
  process.exitCode = 1;
} finally {
  clearTimeout(watchdog);
  await browser.close();
}
