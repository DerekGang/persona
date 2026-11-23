// Drives the web export in a phone-size browser against the mock hub: connect, send in the main chat and see the reply,
// read Agents, open a session, answer a question. Asserts the round trips and saves screenshots.
//   BASE=http://127.0.0.1:8082 MOCK=http://127.0.0.1:18760 OUT=shots node scripts/shots.mjs
// playwright-core comes from web/node_modules (npm ci there); the browser is Playwright's bundled chromium.
import { createRequire } from "node:module";
import { mkdirSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const require = createRequire(join(dirname(fileURLToPath(import.meta.url)), "..", "..", "web", "package.json"));
const { chromium } = require("playwright-core");

const BASE = process.env.BASE ?? "http://127.0.0.1:8082";
const MOCK = process.env.MOCK ?? "http://127.0.0.1:18760";
const OUT = process.env.OUT ?? "shots";
const RUNS = (process.env.RUNS ?? "en:light,en:dark,zh-Hans:light,ja:dark").split(",").map((r) => r.split(":"));
mkdirSync(OUT, { recursive: true });

const ok = (cond, msg) => {
  if (!cond) throw new Error(`FAIL: ${msg}`);
  console.log(`ok   ${msg}`);
};
const get = (path) => fetch(`${MOCK}${path}`, { headers: { Authorization: "Bearer mock" } }).then((r) => r.json());

async function run(locale, scheme) {
  const tag = `${locale}-${scheme}`;
  await fetch(`${MOCK}/mock/reset`).catch(() => {});
  const browser = await chromium.launch({ args: ["--no-sandbox"] });
  const ctx = await browser.newContext({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 2, colorScheme: scheme, locale });
  const page = await ctx.newPage();
  const errors = [];
  page.on("pageerror", (e) => errors.push(String(e)));
  const shot = (name) => page.screenshot({ path: join(OUT, `${tag}-${name}.png`) });
  const words = { en: { connect: "Connect", send: "Send", agents: "Agents" }, "zh-Hans": { connect: "连接", send: "发送", agents: "Agent" }, ja: { connect: "接続", send: "送信", agents: "Agent" } }[locale];

  await page.goto(BASE);
  await page.getByLabel(/./).first().waitFor({ timeout: 20000 });
  await shot("1-connect");
  await page.getByLabel(/^(Access token|访问令牌|アクセストークン)$/).fill("mock");
  await page.getByRole("button", { name: words.connect, exact: true }).click();

  // main chat
  const box = page.getByPlaceholder(/^(Message|发消息|メッセージ)$/);
  await box.waitFor({ timeout: 20000 });
  await page.getByText(/3 件事/).first().waitFor({ timeout: 20000 });
  ok(true, `[${tag}] connected and the main chat's history shows`);
  const text = "What needs me today?";
  await box.fill(text);
  await page.getByRole("button", { name: words.send, exact: true }).click();
  await page.getByText(text).first().waitFor();
  ok(true, `[${tag}] sent message shows at once`);
  const reply = `Two things: the test framework question and a plan to approve (${tag}).`;
  await fetch(`${MOCK}/mock/say?text=${encodeURIComponent(reply)}&stream=1&steps=6&ms=120`);
  await page.getByText(reply).first().waitFor({ timeout: 15000 });
  ok(true, `[${tag}] assistant reply arrived over the socket`);
  await shot("2-chat");

  // agents
  await page.getByRole("tab", { name: new RegExp(words.agents) }).click();
  await page.getByTestId("count-0").waitFor();
  const read = async () => Promise.all([0, 1, 2].map(async (i) => Number((await page.getByTestId(`count-${i}`).innerText()).split("\n")[0])));
  await page.waitForFunction(() => Number(document.querySelector('[data-testid="count-0"]')?.innerText.split("\n")[0]) > 0);
  const shown = await read();
  const hub = (await get("/api/v1/sessions?counts=1")).state;
  ok(shown.join() === [hub.needs_you, hub.working, hub.unread].join(), `[${tag}] Agents counts ${shown} equal the hub's (${hub.needs_you},${hub.working},${hub.unread})`);
  await shot("3-agents");

  // a session that needs you, and its question (the first single-question card the hub still has pending)
  const ix = (await get("/api/v1/interactions")).interactions.find((i) => i.kind === "question" && i.questions?.length === 1 && i.questions[0].options?.length);
  ok(Boolean(ix), `[${tag}] the mock still has an unanswered question (restart the mock between runs)`);
  const label = ix.questions[0].options[0].label.replace(/\s*\(Recommended\)$/, "");
  const option = page.getByRole("radio", { name: new RegExp(label.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")) });
  await page.getByRole("tab", { name: new RegExp(words.agents) }).click();
  await page.getByRole("button", { name: new RegExp(ix.session_title) }).first().click();
  await option.first().waitFor({ timeout: 15000 });
  await shot("4-session-question");
  await option.first().click();
  const before = (await get("/api/v1/interactions")).interactions.length;
  await page.getByRole("button", { name: /^(Send answer|发送回答|回答を送信)$/ }).first().click();
  await option.first().waitFor({ state: "detached", timeout: 15000 });
  const after = (await get("/api/v1/interactions")).interactions.length;
  ok(after === before - 1, `[${tag}] the question was answered at the hub (${before} -> ${after} pending)`);
  await shot("5-answered");

  ok(errors.length === 0, `[${tag}] no page errors${errors.length ? `: ${errors[0]}` : ""}`);
  await browser.close();
}

for (const [locale, scheme] of RUNS) await run(locale, scheme);
console.log("shots: all scenarios passed");
