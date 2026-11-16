---
name: doctor
author: Phelix Chen
description: Use when the user asks to health-check their whole Claude Code setup (settings, CLAUDE.md, rules, skills, hooks, subagents, permissions) against community and official best practices, e.g. "/agent-perform:doctor", "體檢 Claude Code 環境".
disable-model-invocation: true
allowed-tools: Bash("${CLAUDE_SKILL_DIR}/../../bin/ap" doctor)
---

# Claude Code 環境體檢

自動檢查的結果（`bin/ap doctor`，唯讀）：

!`"${CLAUDE_SKILL_DIR}/../../bin/ap" doctor`

## 要做的事

1. 讀 [checklist.md](checklist.md)。上表的 A1–A15 對應清單裡「檢查」欄的 ID；清單標「看設定」的項目，自己讀 `~/.claude/settings.json`、`~/.claude/CLAUDE.md`、`~/.claude/rules/`、`~/.claude/skills/`、`~/.claude/agents/` 判斷；標「習慣」的不打分。
2. 回報格式：
   - 🔴 該改、🟡 可以更好、✅ 已做到，依影響排序，只列值得處理的項目。
   - 每條一句問題＋一句建議，附 checklist 的原始出處連結（只寫原始出處，不寫彙整來源）。
   - 結尾請使用者挑要修的編號。
3. 這個回合只診斷，不改任何檔案；使用者挑了之後再動手，改 settings／CLAUDE.md 前先備份到 `/tmp`。

## Gotchas

- 出處連結只能逐字照抄 checklist.md 的網址；清單只給個人首頁（例如 `https://x.com/bcherny`）就寫首頁加日期，不可自己編 status ID。
- 使用者刻意的選擇不要再列成問題：例如 autocompact 關閉改用提醒（A2 只會顯示 ➖）。上次報告後使用者明講保留的，放 ✅ 或略過。
- `settings.local.json` 的 hooks 要跟 `settings.json` 合併看；只看其中一份會誤報「沒有 Stop hook」。
- A8、A9 會把已啟用 plugin 自帶的 hook 和 subagent 一起算；A7 只看使用者自己的 skill。
- A1 讀的是最新修改的 transcript，跑 doctor 的這個工作階段通常就是它。
- auto mode 分類器可能擋下讀寫 `~/.claude/CLAUDE.md` 的動作（Self-Modification）；被擋就把要改的內容列給使用者，不要繞路。
