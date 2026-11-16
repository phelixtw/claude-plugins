---
name: setup
description: Use right after installing the agent-perform plugin (first-run setup, prompted by its SessionStart reminder), or when the dashboard or the Claude Mod is not working.
version: 2.0.0
author: Phelix Chen
license: MIT
---

# agent-perform setup / troubleshooting

## First run

Run these checks and report each as ✅ / ❌ in one line:

1. Dashboard: `curl -s -m 2 localhost:8787/health`
2. bin/ap: `"$ROOT/bin/ap" --version` where
   `ROOT=$(ls -d ~/.claude/plugins/cache/*/agent-perform/*/ | sort -V | tail -1)`.
   The per-call hooks (poll gate, Claude Mod/statusline, `/context` probe) run as
   `bin/ap`, a Go binary that ships prebuilt in the plugin for Apple Silicon
   (darwin/arm64) only. The Claude Mod (`hooks/auto_recap.tsx`, loaded
   via `modules` in `hooks/hooks.json`) runs `bin/ap scorecard --mod`, so it
   needs this check too.

Fix any ❌ with the matching section below. When the checks pass, run
`touch ~/.claude/agent-perform-setup-done` so the SessionStart reminder stops,
and tell the user the Claude Mod appears above the prompt from the next
session. If any leftover listed under "Old installs" exists, mention it so the
user can remove it by hand.

Most of this plugin wires itself up on the first session after install: the
hooks register via `hooks/hooks.json`, and a `SessionStart` hook starts the
`localhost:8787` dashboard; the live display is the Claude Mod above the
prompt. This doc only covers what that automation cannot safely do for you.

## Statusline (only where the Claude Mod does not load)

The Claude Mod is the live display, so nothing wires a statusline.
To get the same rows as a statusline, add this to
`~/.claude/settings.json` yourself:

```json
{
  "statusLine": {
    "type": "command",
    "command": "\"$(ls -d $HOME/.claude/plugins/cache/*/agent-perform/*/ | sort -V | tail -1)bin/ap\" statusline"
  }
}
```

If you want both statuslines, most people just have their existing script
pipe the same stdin into this one's `bin/ap scorecard` as a second command.

## bin/ap does not run

`bin/ap` is built for Apple Silicon (darwin/arm64) only. On another platform
(`uname -sm` is not `Darwin arm64`) the Claude Mod, the poll gate and the `/context`
probe stay off; tell the user this platform is not supported. On Apple Silicon,
a "bad CPU type" or "killed" error means the plugin copy is damaged: reinstall
the plugin.

## Dashboard not showing anything at localhost:8787

Check it's actually running:

```bash
curl -s localhost:8787/health
```

If that fails, the `SessionStart` hook (`bin/ap session-start dashboard`) should have
started it — check its log:

```bash
tail -50 ~/.claude/agent-perform-dashboard.log
```

and start it by hand if needed:

```bash
"<plugin path>/bin/ap" dashboard --serve 8787 &
```

A brand-new install starts with an empty dashboard — it fills in as you work
(each turn, each subagent dispatch).
