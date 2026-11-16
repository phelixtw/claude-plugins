# phelix-plugins

Claude Code plugins by Phelix Chen.

| Plugin | What it does |
|---|---|
| [agent-perform](plugins/agent-perform) | A Claude Mod above the prompt that keeps your context spending in check and builds good habits (live context use, Cache countdown, quota, one-click `/clear` `/compact` `/recap`), plus a local dashboard at `localhost:8787` that drills into every conversation and turn to find where tokens were spent abnormally. |
| [archify-review](plugins/archify-review) | Reviews a commit or a Jira ticket's commits as an explorable standalone HTML report: an architecture diagram of the change, net diffs per flow with test coverage and Given / When / Then cases, reviewed checkboxes, and Code Review / Security / Code Smell TODOs. |

## Install

```
/plugin marketplace add phelixtw/claude-plugins
/plugin install agent-perform@phelix-plugins
/plugin install archify-review@phelix-plugins
```

Each plugin's README covers setup and usage (in Traditional Chinese).

## License

MIT. See each plugin's `LICENSE`; archify-review is based on the open-source [archify](https://github.com/tt-a1i/archify).
