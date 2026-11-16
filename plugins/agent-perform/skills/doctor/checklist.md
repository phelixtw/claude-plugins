# Claude Code 環境檢查清單

整理自 2026-10-01 的社群 tips 彙整，每條只標原始出處。
「檢查」欄的 `A?` 是 `bin/ap doctor` 會自動檢查的項目 ID，寫「看設定」的由 Claude 讀設定判斷，寫「習慣」的只在報告附錄提醒，不打分。

## Context 與工作階段
| # | 原則 | 檢查 | 原始出處 |
|---|---|---|---|
| 1 | 1M context 約 300–400k 開始 context rot，重要工作別放任超過 | A1 | Thariq (@trq212) X 貼文 2026-04-16〈Session Management & 1M Context〉 https://x.com/trq212 |
| 2 | 40% 以下最安全，到 60% 就準備收；換任務時手動 /compact 或 /clear | A1 | Dex Horthy, MLOps Community 2026-03-24 https://youtu.be/YwZR6tc7qYg?t=1541 |
| 3 | /compact 帶提示（focus on X, drop Y）勝過等 autocompact | A2 | Thariq 2026-04-16（同 1） |
| 4 | rewind 勝過糾正：失敗的嘗試用 /rewind 退回，不要留在 context | 習慣 | Thariq 2026-04-16（同 1） |
| 5 | 用 subagent 管 context：只需要結論的工作交給 subagent | A9 | Thariq 2026-04-16（同 1） |
| 6 | 新任務開新工作階段；compact 有損、/clear＋交接摘要可控 | 習慣 | Thariq 2026-04-16（同 1） |
| 7 | 內建 recaps 關掉（`awaySummaryEnabled: false`）；agent-perform 的 auto-recap 在 context 30% 以上、回覆結束時，在輸入欄上方提示要 `/recap` 還是 `/compact`；選項 `autoCompact`（預設關）打開後，context 60% 以上閒置 1 小時會直接用 recap compact | A3 | 本 plugin `hooks/auto_recap.tsx`；recap 功能本身見 Boris Cherny (@bcherny) X 貼文 2026-04-16〈6 tips for Opus 4.7〉 https://x.com/bcherny |
| 8 | /rename 重要工作階段，之後 /resume | 習慣 | Cat Wu, Every podcast https://every.to/podcast/how-to-use-claude-code-like-the-people-who-built-it |

## CLAUDE.md 與 rules

| # | 原則 | 檢查 | 原始出處 |
|---|---|---|---|
| 9 | 每個 CLAUDE.md 控制在 200 行內 | A4 | 官方文件 https://code.claude.com/docs/en/memory#write-effective-instructions ；Boris https://x.com/bcherny/status/2007179840848597422 |
| 10 | `.claude/rules/*.md` 每次都載入；加 `paths:` frontmatter 改成碰到相符檔案才載入 | A5 | 官方文件 https://code.claude.com/docs/en/memory#organize-rules-with-claude/rules/ |
| 11 | 領域專屬規則寫明適用條件與理由（「改 X 時…，因為…」），不靠強調標籤或大寫提高權重 | 看設定 | 官方文件 https://code.claude.com/docs/en/memory#write-effective-instructions |
| 12 | 大型規則拆到 `.claude/rules/` | A4 | 官方文件（同 10） |
| 13 | monorepo 用多層 CLAUDE.md | 看設定 | Boris https://x.com/bcherny/status/2016339448863355206 |
| 14 | 把遷移做完，半套的框架會讓模型選錯寫法 | 習慣 | Boris, YouTube https://youtu.be/julbw1JuAz0?t=1112 |
| 15 | CLAUDE.md 是 context 不是強制設定；能交給 harness 硬性執行的（attribution、permissions、model）放 settings.json | A6 | 官方文件 https://code.claude.com/docs/en/memory ； https://code.claude.com/docs/en/settings |

## Subagent、Command、Skill

| # | 原則 | 檢查 | 原始出處 |
|---|---|---|---|
| 16 | 依功能分 subagent 並搭配 skill，不要只有籠統的 qa／backend | A9 | Boris https://x.com/bcherny/status/2007179850139000872 |
| 17 | 說「use subagents」多丟算力，主 context 保持乾淨 | 習慣 | Boris https://x.com/bcherny/status/2017742755737555434 |
| 18 | 平行開發用 agent teams＋git worktree | 習慣 | Boris https://x.com/bcherny/status/2025007393290272904 |
| 19 | test-time compute：另開 context 的 agent 抓得到前一個的 bug | A9 | Boris https://x.com/bcherny/status/2031151689219321886 |
| 20 | 每天做好幾次的流程做成 command／skill | 看設定 | Boris https://x.com/bcherny/status/2007179847949500714 、 https://x.com/bcherny/status/2017742748984742078 |
| 21 | `context: fork` 讓 skill 在隔離的 subagent 跑，主 context 只看到結果 | 看設定 | Lydia Hallie https://x.com/lydiahallie/status/2033603164398883042 |
| 22 | skill 是資料夾：用 references/、scripts/、examples/ 做漸進揭露 | A7 | Thariq〈How we use skills〉 https://x.com/trq212/status/2033949937936085378 |
| 23 | 建議 skill 加一段 Gotchas，記下 Claude 常踩的坑，遇到新的再補上 | A7 | Thariq（同 22） |
| 24 | description 是觸發條件，不是摘要（寫「什麼時候該用」） | A7 | Thariq（同 22） |
| 25 | skill 不寫廢話，只寫能把 Claude 推離預設行為的內容 | 看設定 | Thariq（同 22） |
| 26 | skill 給目標與限制，不要一步一步硬規定 | 看設定 | Thariq（同 22） |
| 27 | skill 附 script 與 library，讓 Claude 組合而不是重寫 | A7 | Thariq（同 22） |
| 28 | SKILL.md 用 `!command` 注入動態輸出 | 看設定 | Lydia Hallie https://x.com/lydiahallie/status/2034337963820327017 |
| 29 | SKILL.md 本體保持 500 行內，細節拆到參考檔 | A7 | 官方 skill 撰寫指南 https://platform.claude.com/docs/en/agents-and-tools/agent-skills/best-practices |

## Hooks

| # | 原則 | 檢查 | 原始出處 |
|---|---|---|---|
| 30 | skill 內掛 on-demand hook（/careful 擋破壞性指令） | 看設定 | Thariq（同 22） |
| 31 | 用 PreToolUse hook 統計 skill 用量，找出沒觸發的 skill | A8 | Thariq（同 22） |
| 32 | PostToolUse hook 自動 format | 看設定 | Boris https://x.com/bcherny/status/2007179852047335529 |
| 33 | 權限請求交給 hook 判斷、自動放行安全的 | A10 | Boris https://x.com/bcherny/status/2017742755737555434 |
| 34 | Stop hook 在回合結束時驗證或催它繼續 | A8 | Boris https://x.com/bcherny/status/2021701059253874861 |
| 35 | hook 指向的 script 必須存在（壞掉會靜默失敗） | A8 | 官方文件 https://code.claude.com/docs/en/hooks |

## 權限與模型

| # | 原則 | 檢查 | 原始出處 |
|---|---|---|---|
| 36 | 用 auto mode 取代 dangerously-skip-permissions | A10 | Boris 2026-04-16（同 7）；官方 https://code.claude.com/docs/en/permission-modes#eliminate-prompts-with-auto-mode |
| 37 | /permissions 用萬用字元白名單（`Bash(npm run *)`） | A10 | Boris https://x.com/bcherny/status/2007179854077407667 |
| 38 | /sandbox 隔離檔案與網路，減少權限提示 | A10 | Boris https://x.com/bcherny/status/2021700506465579443 |
| 39 | settings 裡引用的檔案必須存在 | A11 | 官方 auto mode 文件（同 36）：分類器會讀 environment 描述 |
| 40 | 規劃用 Opus、寫 code 用 Sonnet | A9 | Cat Wu https://x.com/_catwu/status/1955694117264261609 |
| 41 | 依任務調 effort（low…max） | A12 | Boris 2026-04-16（同 7） |
| 42 | 依任務調 effort 控制思考深度（thinking 一律開啟） | A12 | Boris https://x.com/bcherny/status/2007179838864666847 |

## 工具與日常

| # | 原則 | 檢查 | 原始出處 |
|---|---|---|---|
| 43 | 用 statusline 隨時看 context | A13 | Boris https://x.com/bcherny/status/2021700784019452195 |
| 44 | 用瀏覽器 MCP（Chrome DevTools／Playwright）讓 Claude 自己看 console | A14 | 官方 https://code.claude.com/docs/en/chrome |
| 45 | 每天更新 Claude Code | A15 | 官方安裝文件 https://code.claude.com/docs/en/setup |
| 46 | `claude doctor` 檢查安裝與認證 | A15 | 官方 CLI 文件 https://code.claude.com/docs/en/cli-reference |
| 47 | 常駐 recurring 監看用 /loop，機器關了也要跑用 /schedule | 習慣 | Boris https://x.com/bcherny/status/2038454341884154269 |

## 習慣類（只提醒，不檢查）

- 先進 plan mode；用 AskUserQuestion 讓 Claude 訪談你，再開新工作階段執行。出處：Boris https://x.com/bcherny/status/2007179845336527000 ；Thariq https://x.com/trq212/status/2005315275026260309
- 每個 phase 都有測試；PRD 切成 vertical slice。出處：Dex https://youtu.be/YwZR6tc7qYg?t=1032 ；Matt Pocock https://youtu.be/-QFHIoCo-Ko
- 開第二個 Claude 當 staff engineer 審 plan；寫清楚 spec 再交辦。出處：Boris https://x.com/bcherny/status/2017742745365057733 、 https://x.com/bcherny/status/2017742752566632544
- 「grill me」「prove it works」；平庸修法後「scrap this and implement the elegant solution」；貼 bug 說 fix 就好。出處：Boris https://x.com/bcherny/status/2017742752566632544 、 https://x.com/bcherny/status/2017742750473720121
- PR 小而專一、squash merge。出處：Boris https://x.com/bcherny/status/2038552880018538749
- /code-review 做多 agent PR 審查。出處：Boris https://x.com/bcherny/status/2031089411820228645
- agentic search（glob＋grep）勝過 RAG。出處：Boris https://youtu.be/julbw1JuAz0?t=3095
- 原型勝過 PRD，多做幾版。出處：Boris https://youtu.be/julbw1JuAz0?t=3630
