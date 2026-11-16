// Code generated from scripts/dash_pages.py tables.

package dash

type term struct{ term, body string }

type termGroup struct {
	group string
	items []term
}

var terms = []termGroup{
	{"浪費", []term{
		{"浪費", "總覽、單日明細、對話三頁最前面的數字。浪費＝「context 太長」＋「做太久被中止」＋「Cache 過期重寫」，是花費裡<b>改用法就能降低</b>的部分。這是上限估計，適合用來排優先順序，實際省得下來的金額會比較少。"},
		{"context 太長", "context 超過視窗 40% 的回合，每回合花費減掉你 150–300k 回合的一般花費後加總。基準用 150–300k、不用最便宜的 &lt;150k：<code>/clear</code> 之後 context 還是會長回來，150–300k 是拆開後比較可能落到的花費。"},
		{"Cache 過期重寫", "回合開始距上一個 API 呼叫已超過該對話的 Cache 存活時間（1 小時或 5 分鐘），而且這個回合第一次 API 呼叫的 cache 寫大於 cache 讀、至少 5,000 token。金額＝那次 cache 寫 token ×（cache 寫價 − cache 讀價）。"},
		{"Tag（逐回合明細）", "這個回合踩到的問題：<b>context 太長</b>（超過視窗 40%）、<b>做太久被中止</b>、<b>重複呼叫</b>、<b>工具上限</b>（Read、Bash 輸出／逾時、Edit、Glob、WebFetch、PDF 碰到 Claude Code 的上限）、<b>Cache hit</b>（Cache 過期重寫的金額佔這個回合花費 10% 以上才標示，出處是官方 /usage 的 10% 規則）。「—」代表這個回合沒有問題。"},
	}},
	{"單位", []term{
		{"對話", "一個 Claude Code session（一份 transcript），也就是 <code>claude --resume</code> 的單位。<code>/clear</code> 或關掉視窗就換一個。"},
		{"回合", "你送出一則輸入，到 Claude 回完為止。工具回傳、斜線指令、系統注入的訊息都不算一個回合。"},
		{"本回合 context 增加", "狀態列「本回合 context +86k（平常 +6k）」：本回合最大一次 API 呼叫的 context，比上一個回合多了多少。平常＝這個對話先前每回合 context 增加量，由小排到大正中間那一筆；先前不到 3 個回合時不顯示。"},
		{"API 呼叫", "回合裡 Claude 每用一次工具就多一次 API 呼叫：Claude 送出、工具執行、結果回傳、Claude 再繼續。每次 API 呼叫都會把整個 context 重送一次。"},
	}},
	{"花費", []term{
		{"skill 常駐 context 的花費", "skill 全文載入後，之後每個回合以 cache 讀價重讀的花費合計。載入那一次很便宜，花費來自之後的重讀。"},
		{"重複呼叫", "分子：判成重複呼叫的回合數。分母：這個對話的總回合數。每個回合只跟前 3 個回合比對；跟整個對話比對的話，CLAUDE.md 這類每個對話都會讀一次的檔案，會讓每個回合都被判成重複呼叫。符合下面任一種就算：① 同一個 Bash 指令又跑一次；② 同一個 <code>Skill</code> 或 MCP 工具又叫一次、參數也一樣；③ 沒改過的檔案又 <code>Read</code> 一次（中間被 <code>Edit/Write</code> 改過就不算）、<code>WebFetch</code> 抓同一個網址、<code>Glob/Grep</code> 用同一個 <code>pattern|path</code>；④ <code>Edit/Write</code> 寫的新內容跟前面寫過的內容高度相似（difflib 相似度 ≥ 0.8，比對前 2000 字元，短於 40 字的內容不算）。"},
	}},
}

var skillFields = setOf("agent", "allowed-tools", "argument-hint", "arguments", "background", "compatibility", "context", "description", "disable-model-invocation", "disallowed-tools", "effort", "hooks", "license", "metadata", "model", "name", "paths", "shell", "user-invocable", "when_to_use")

const syncedSrc = "claude.ai 同步"
const bundleNote = "這裡只標出位置。判斷交給你自己的 AI：複製下面的資料與原始樣本，貼進你的 CLI。"
const scriptsCardDim = "<p class=\"dim\">依「對話數 × token」排序，只列前 15 種。腳本類型＝去掉參數、路徑、ID、字串後的樣子；呼叫 Jira、Confluence、Slack、GitLab 等 API 的腳本，依服務與路徑歸成一類，端點裡的 * 是 ID。滑鼠移到類型上看第一行有內容的程式碼。估計花費只算寫出腳本（output）與腳本輸出第一次進 context（input），不含之後每回合重讀。</p>"
const repeatCardDim = "<p class=\"dim\">判定規則：跟同一個對話的前 3 個回合比，同一個指令、skill/MCP 工具、檔案或網址又出現，或寫入內容高度相似。花費與 token 是涉及回合的整個回合，不是重複那一次呼叫本身，所以是上限。「同一個 API 反覆呼叫」「輪詢」歸在「反覆確認服務」那一類；錯誤後重試目前沒有資料可以判斷，不列。</p>"
const fixedDetailA = "<p>token 數與 <code>/pipeline?turn=1</code> 的 Skill / subagent / MCP 清單、CLAUDE.md / rules / memory，加上 Hook 輸出裡的 SessionStart 加總相同；UserPromptSubmit、PostToolUse、Stop 的 hook 每次觸發都注入新的內容，不算固定載入。金額＝該 token × 回合數 × cache 讀價，不含第 1 回合的 cache 寫。</p><p><a href=\"/optimize\">Optimize</a> 頁可以健檢 CLAUDE.md 與 skill。"
const delegateDetail = "<p>判斷標準是探索過程要不要留在主對話：留下來的內容，之後每個回合都要重讀。</p>"
const modelDetail = "<p>各模型的每回合平均花費差很多，但花費低的模型，回合的 context 本來就比較小（右欄「context 一般」），價差不全來自模型本身。</p>"
