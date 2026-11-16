# agent-perform：產品範圍

這份文件取代舊的 flow.html 和 progress.html，舊檔不再保留。之後開發時，每次從「待修清單」挑一項，用 `/pstack:poteto-mode` 接手。做完就把那一項從清單刪掉，git 歷史會留下紀錄。

## 定位

- 目標使用者：在 Apple Silicon Mac 上用 Claude Code 的個人使用者。
- 主打：Claude Mod 在輸入框上方幫你控管 context 的支出，養成好習慣；另附一個龐大的後台，可以細看每一個對話，找出異常的 token 花費在哪裡。
- 要解決的問題：Token 和花費被哪個對話、哪個回合、哪個習慣用掉。要從 context、工具、委派、模型選擇裡找出浪費，再用「改動前後」驗證設定改了有沒有效。
- 資料只在本機處理，不存 prompt 原文。
- 原則：一頁只講一個主題，每頁版型相同。同一個數字只有一個來源，每個結論都附證據連結。

## 現在的版本有的功能

- 儀表板（`127.0.0.1:8787`，`ap dashboard --serve`）。
  - 頁面：總覽、Context、其他 Tag、委派、模型、改動前後、最貴的回合、優化、怎麼算，另有對話、單日、單一回合頁。
  - 優化頁會跑 `/claude-api prompt-audit`，用訂閱額度，只給建議，不改檔。
- Claude Mod（`hooks/auto_recap.tsx`，資料來自 `bin/ap scorecard --mod`），顯示在輸入框上方，Claude CLI 和 Claude Desktop 都看得到。
  - 內容：模型、額度、context 用量、Cache 倒數、本回合 context 增加多少、這個回合派出的 subagent、正在做的事。
  - context 達 30% 時多一列 `/clear`、`/compact`、`/recap`。`autoCompact` 開啟時，context 達 60% 且閒置 1 小時會自動 recap 再 compact。
- Hooks。
  - SessionStart：dashboard、nudge。
  - PreToolUse(Bash)：poll gate，擋掉前景的 sleep 輪詢。
  - Stop：ctx-probe。
  - SessionEnd：整理委派花費、重複呼叫與 token 模型。
- Skills：setup、doctor。
