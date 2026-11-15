# Hidden Agent directories: browser evidence

Synthetic browser fixtures only; no production hub, user sessions or credentials.

`web/e2e/hidden-agent-dirs.mjs` exercises per-computer rule toggling and the temporary
inspection list against an exclusively owned loopback Vite server. Every hub HTTP
and WebSocket request is intercepted with synthetic data. The switch is enabled,
disabled, then enabled again; the inspection view includes a hidden session while
the normal Agents list excludes it. No Agent is started or messaged.

```sh
node web/e2e/hidden-agent-dirs.mjs <loopback-web-origin> <artifact-directory> <chromium-executable>
```

Result: `PASS hidden directory settings toggle and temporary inspection, light/dark`.
Both themes were visually inspected at 1280 × 900. The settings page reuses Group,
Row, Switch, Button and the settings field style; inspection reuses Dialog and Row.
Screenshots do not prove deployment, Apple rendering, or real phone approval.

- [Settings, light](settings-light.png)
- [Settings, dark](settings-dark.png)
- [Inspection, light](inspection-light.png)
- [Inspection, dark](inspection-dark.png)
