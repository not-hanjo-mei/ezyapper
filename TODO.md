# TODO

## WebUI

- [ ] Known issue: WebUI dashboard is currently buggy/unstable.
- [ ] Better UI.
- [ ] Reproduce and document top failure paths (config update, plugin toggle, logs view, auth/login flow).
- [ ] Fix critical UI/API mismatch and error-handling regressions first.
- [ ] Add a WebUI smoke checklist (desktop/mobile, auth, dashboard load, basic API actions).
- [ ] Remove temporary disable recommendation from docs after fixes are verified.

## MCP

- [ ] Known issue: MCP works, but related runtime logs are not consistently visible.
- [ ] Add startup logs for MCP connect status per server (success/failure + reason).
- [ ] Add summary logs after MCP tool registration (total tools and source server).
- [ ] Add per-call debug logs for MCP tool execution (tool name, latency, result/error).
- [ ] Add a verification checklist/test to confirm logs appear when MCP is enabled.
