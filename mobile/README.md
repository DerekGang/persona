# Persona mobile demo (React Native / Expo)

A working demo of one way to build the phone apps: a React Native (Expo) client that shares the web client's pure
TypeScript, draws native controls, and talks to a Persona hub. It exists so maintainers can judge the feel, the reuse and
the effort (issue #13511). **It is not merged, has no store release, and is not a product.**

Audience: maintainers. Everyday users never see this directory.

## What it does

- **Connect**: hub address plus an access token or `persona://pair` link; the token is traded for this phone's own device
  token like the web does. Saved in the secure store (Keychain / Keystore); on the web target it lives in the tab only.
- **Main chat**: the assistant's conversation (found through `GET /assistant`), send a message, see it at once as
  Sending, then Sent, and watch the reply stream in over the socket.
- **Agents**: Needs you / Working / Unread counts, a Needs you section first, then one section per project, the web's plain
  state words, pull to refresh. Needs you is also the badge on the Agents tab.
- **A session**: its conversation, a reply box, and its pending question, plan or permission card. Answering sends the
  same `POST /interactions/{id}/resolve` body as the web.
- **en, zh-Hans, ja** and **light and dark** from the start (the device decides).

## Run it against the mock hub

The mock hub needs Python 3.11 and no credentials. From the repository root:

```sh
PORT=18760 MOCK_ECHO_SENT=1 MOCK_SESSIONS_LIST=1 MOCK_SESSIONS_CATALOG=1 MOCK_MANAGERS=1 MOCK_PAGE_COUNTS=1 \
  python3.11 scripts/mock/mock_hub.py &

cd mobile
npm ci
npm run typecheck          # type-checks the app, the shims and the tests
npm test                   # unit tests, then the parity test under the web's own vitest
npx expo export --platform web
MOCK_HUB_URL=http://127.0.0.1:18760 PORT=8082 npm run serve   # static server + proxy to the mock
```

Open `http://127.0.0.1:8082` at a phone size, enter any token (the mock accepts `mock`), and tap Connect. The proxy keeps
the browser on one origin because the mock has no CORS. On a device, enter your own hub's address instead.

Screenshots and the round trips (connect, send and receive, counts equal the hub's, answer a question) run headless:

```sh
cd web && npm ci && cd ../mobile            # playwright-core and Playwright's chromium
BASE=http://127.0.0.1:8082 MOCK=http://127.0.0.1:18760 OUT=shots RUNS=en:light,en:dark node scripts/shots.mjs
```

Restart the mock before each run: answering a question is permanent for that mock process.

On a phone or emulator: `npx expo start` (Expo Go cannot load every native module here; a development build is the
honest route), or `npx expo prebuild -p android && cd android && ./gradlew assembleDebug`.

## What is shared with the web

Nothing in `web/` is copied or edited. Metro (`metro.config.js`) and Vitest (`vitest.config.mts`) import `web/src` in place
through the `@web/` alias, with packages resolved from `mobile/node_modules` only, so there is one React.

Imported as-is (22 files, about 3,850 lines in the bundle, plus `lib/types.ts`, 1,142 lines of wire types):

| Module | What the app takes from it |
| --- | --- |
| `lib/types.ts` | `Session`, `Item`, `Interaction`, `Machine`, `Question` |
| `lib/format.ts` (+ agent-kind, closeout, usage-limit, work) | `stateOf`, `byAttention`, `sessionTitle`, `sessionPreview`, `ago` |
| `lib/agent-repos.ts` (+ herdr-tree, agent-groups, workspace-catalog) | `repoOf`: which project and worktree a session belongs to |
| `lib/state-labels.ts` | the plain state words and counts ("2 need you") |
| `lib/reconnect.ts` | resume cursor (`wsQuery`, `afterHello`, `afterEvent`) and reconnect backoff (`retryDelay`) |
| `lib/auth.ts` | `pairToken`: a bare token, a pairing link or a URL with `?token=` |
| `lib/tool-detail.ts` | how an option label reads |
| the web's locale JSON | every `lib-*` string, in en, zh-Hans and ja, built by `scripts/build-catalog.mjs` |

**Seams.** A few web modules touch the DOM or the web runtime. Metro replaces them by file, so the shared modules stay
untouched: `i18n/index.ts` (uses `import.meta.glob`), `lib/api.ts`, `lib/base.ts` and `lib/manager-choice.ts` become the
files in `src/shims/`. Any `.tsx` file or anything under `web/src/ui` or `web/src/views` is a build error on purpose.

**Rewritten small** (the web's version is bound to the page): the store (`src/runtime/store.ts`; the web's is
`lib/store.ts`), the HTTP client, the socket wrapper, and `src/domain/counts.ts`, a copy of `stateCounts` (which lives in
`lib/sessions.ts` beside React hooks and the whole store). `parity/counts.parity.test.ts` runs the web's own `stateCounts`
and the copy on the same sessions, so the two cannot drift silently.

`src/theme.ts` is a demo-only mirror of a few web design tokens; the real app would generate it from one token source.

## Layout

```
app/                  expo-router screens: connect, (tabs) Chat and Agents, session/[machine]/[sid]
src/runtime/          hub client: storage, client (HTTP), socket (WebSocket), store, actions
src/domain/           selectors over the shared web logic: counts, grouping, transcript
src/shims/            stand-ins for the web's DOM-bound seam modules
src/ui/               Card, Button, AgentRow, ChatView, Composer, QuestionCard
src/i18n/             the app's own strings (en, zh-Hans, ja) behind the web's t()
scripts/              catalog build, typecheck, demo server, screenshot runs
test/, parity/        unit tests; the parity test runs under the web's vitest config
```

## Known gaps (demo scope)

No Markdown rendering, images, files, folded work detail, history beyond the last two turns plus "Load earlier",
settings, push, share sheet, phone tools, Live Activity, widgets, or elicitation, screen and secret cards. Plan and
permission cards have basic buttons only. Strings use the web's namespaces where they exist and `mobile.*` for the rest;
the demo does not run the web's i18n checks.
