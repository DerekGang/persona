# Scheduled task execution form

The synthetic browser fixture uses intercepted HTTP and WebSocket responses exclusively; it contacts no real hub,
task or model. Review images belong to [PR #6806](https://github.com/nwverse/persona/pull/6806), not the source tree.

Run against an owned loopback Vite server, writing captures outside the checkout:

```sh
node web/e2e/task-execution.mjs http://127.0.0.1:5173 \
  --shots <capture-directory> --chromium <chromium-executable>
```

The fixture covers Cron, inherited and explicit execution summaries before the prompt, save/reopen, directory choices,
source-chat validation/toggle/clear and unavailable-launcher recovery. It checks desktop and phone widths in light and
dark themes, doubles text size, verifies controls remain within the dialog and records page errors. The largest-text
form scrolls with its footer available. Model discovery loading/failure is checked after changing computers; an older
context's model must not remain selectable. Deferred-response component tests also cover late responses after failure.

The form reuses the existing dialog, fields, native selects, model picker and semantic tokens. Advanced options stay
collapsed by default. Follow the repository's screenshot-in-PR workflow when publishing captures.
