package dash

import "strings"

// The 怎麼省 tutorial under each tag of the 其他 Tag page: what goes wrong, a before/after picture,
// up to three steps, each step with the official doc it comes from. Texts are trusted HTML.

type howStep struct{ text, src, url string }

type howTo struct {
	what       string
	before     string // the before/after picture, one line each
	after      string
	steps      []howStep
	extraTitle string // optional block after the steps
}

const (
	docTools     = "https://code.claude.com/docs/en/tools-reference"
	docCosts     = "https://code.claude.com/docs/en/costs"
	docEnv       = "https://code.claude.com/docs/en/env-vars"
	docSkills    = "https://code.claude.com/docs/en/skills"
	docSkillBP   = "https://platform.claude.com/docs/en/agents-and-tools/agent-skills/best-practices"
	docWorktrees = "https://code.claude.com/docs/en/worktrees"
	docSubagents = "https://code.claude.com/docs/en/sub-agents"
	docPerms     = "https://code.claude.com/docs/en/permissions"
	docBest      = "https://code.claude.com/docs/en/best-practices"
)

var tagHowTo = map[string]howTo{
	"repeat": {
		what:   "幾輪內又做同一件事：重讀沒改的檔、重跑同一個指令。每做一次，整段對話就重送一次。",
		before: "第 1 輪 載入環境→做事　第 2 輪 載入環境→做事　第 3 輪 載入環境→做事",
		after:  "開場載入一次　第 1 輪 做事　第 2 輪 做事　第 3 輪 做事",
		steps: []howStep{
			{"CLAUDE.md 寫了「每次先執行某指令」，Claude 每輪都會照做。環境變數改用 <code>CLAUDE_ENV_FILE</code>，開場載入一次。", "Tools reference", docTools},
			{"換到不相關的工作就 <code>/clear</code>。", "Manage costs", docCosts},
			{"測試、建置指令用 hook 只留失敗的行，看一次就懂。", "Manage costs", docCosts},
		},
	},
	"scripts": {
		what:   "Claude 每次查 Jira、Slack 都現場寫小程式，寫一次付一次。做成腳本後只有結果佔額度。",
		before: "每次：寫程式 → 執行 → 讀整包 JSON",
		after:  "執行 <code>scripts/get_issue.sh KEY</code> → 讀幾個重點欄位",
		steps: []howStep{
			{"從上表挑常出現的類型，例如 <code>API|Jira|issue/*</code>。", "", ""},
			{"跟 Claude 說：「把這個查詢做成 skill 的 <code>scripts/</code> 腳本，只輸出重點欄位。」", "Skill 撰寫指南", docSkillBP},
			{"skill 的 <code>allowed-tools</code> 寫 <code>Bash(${CLAUDE_SKILL_DIR}/scripts/*)</code>，就不會跳權限提示。", "Skills", docSkills},
		},
	},
	"bash_timeout": {
		what:   "指令超過 2 分鐘會被移到背景，Claude 接著一直問「好了沒」，每問一次就重送整段對話。",
		before: "跑 → 問 → 問 → 問 → 完成（每個「問」都是一次計費）",
		after:  "丟到背景跑 …… 完成通知 → 看結果",
		steps: []howStep{
			{"CLAUDE.md 加一行：「長指令一開始就用背景執行，等完成通知，不要 sleep 檢查。」", "Tools reference", docTools},
			{"要盯 log 或 CI，叫它用 Monitor，有變化才回報。", "Tools reference", docTools},
			{"常跑 2～10 分鐘的指令，在 settings 的 <code>env</code> 調高 <code>BASH_DEFAULT_TIMEOUT_MS</code>。", "Environment variables", docEnv},
		},
		extraTitle: "agent-perform 已經替你擋下前景輪詢",
	},
	"bash_out": {
		what:   "指令一次印超過約 3 萬字，Claude 只拿到開頭，要再開檔讀一次；那堆字還會留在對話裡，之後每輪都重算。",
		before: "<code>npm test</code>",
		after:  "<code>npm test 2>&1 | grep -A 5 -E '(FAIL|ERROR)' | head -100</code>",
		steps: []howStep{
			{"叫它只看重點：只要失敗的行、最後 50 行或幾個欄位（<code>| tail -50</code>、<code>| jq '.id'</code>）。", "Tools reference", docTools},
			{"跑測試、讀長 log 交給 subagent，只帶摘要回來。", "Manage costs", docCosts},
			{"常跑的測試指令，設 PreToolUse hook 自動只留失敗的行。", "Manage costs", docCosts},
		},
	},
	"edit": {
		what:   "Claude 改檔要先找到「原本那段字」才能換。檔案被別的視窗或格式化工具改過就對不上，只好重讀再改。",
		before: "兩個 Claude 改同一個資料夾 → 內容對不上 ✗",
		after:  "<code>claude -w a</code>、<code>claude -w b</code> 各改自己的副本 ✓",
		steps: []howStep{
			{"同一個專案開好幾個 Claude 時，每個用 <code>claude -w 名稱</code> 開自己的 worktree。", "Worktrees", docWorktrees},
			{"要改的段落先讓它完整讀過；只讀到一部分不算讀過。", "Tools reference", docTools},
			{"講清楚檔案和行號，它就會帶足前後文，不會一次對到兩處。", "Tools reference", docTools},
		},
	},
	"fetch": {
		what:   "讀網頁沒一次讀到：短網址換了網域，工具不會跟過去；或頁面超過 10 萬字只讀到前段。",
		before: "短網址 → 換網域停住 → 再抓一次",
		after:  "最終網址 → 一次到位",
		steps: []howStep{
			{"貼最終網址，不貼短網址。解開短網址：<code>curl -sIL -o /dev/null -w '%{url_effective}' 短網址</code>", "Tools reference", docTools},
			{"講清楚要哪一段，例如「只看安裝章節的指令」，不要只說「看一下這頁」。", "Tools reference", docTools},
			{"頁面很長就叫它用 <code>curl</code> 存成檔，再搜尋需要的部分。", "Tools reference", docTools},
		},
	},
	"read": {
		what:   "檔案超過讀取上限（約 2.5 萬 token 或 256 KB），Claude 只好分好幾段讀，同一份內容一直進對話。",
		before: "讀整份 → 太大被拒 → 第 1 段、第 2 段、第 3 段……",
		after:  "先搜尋 ERROR → 只讀前後 50 行",
		steps: []howStep{
			{"先搜尋再讀：「在 app.log 搜尋 ERROR，只讀前後 50 行」。", "Tools reference", docTools},
			{"知道位置就講行號：「讀 data.json 第 800～900 行」。", "Tools reference", docTools},
			{"PDF 講頁數：「只看第 5～9 頁」，一次最多 20 頁。", "Tools reference", docTools},
		},
	},
	"glob": {
		what:   "找檔案範圍太大，上百個檔名塞進對話，之後每輪都重算。macOS 上 Claude 多用 find、grep，所以少見。",
		before: "<code>**/*</code> → 500 個檔名",
		after:  "<code>src/api/**/*.ts</code> → 12 個檔名",
		steps: []howStep{
			{"提問就講範圍：「只看 src/api 底下的 .ts」。", "Tools reference", docTools},
			{"不知道東西在哪，說「用 Explore subagent 找，只回報檔名和一行說明」。", "Subagents", docSubagents},
			{"node_modules、dist 在 settings 加 <code>permissions.deny</code>：<code>Read(./node_modules/**)</code>。", "Permissions", docPerms},
		},
	},
	"slow": {
		what:   "回合等太久被你中止，花費照算，卻沒有結果。多半是需求太籠統，Claude 走了很遠才發現方向不對。",
		before: "「幫我修好登入」→ 等很久 → 中止，白花",
		after:  "「login.ts 的 token 過期要重新登入，修完跑測試」→ 一次到位",
		steps: []howStep{
			{"講清楚檔案、症狀和怎樣算完成，並給它驗證的方法（測試、預期輸出）。", "Best practices", docBest},
			{"複雜的工作先開 plan mode，方向對了再動手，避免白做。", "Manage costs", docCosts},
			{"太久沒進展就早點中止，改成更小的步驟重來。", "Manage costs", docCosts},
		},
	},
	"search": {
		what:   "你已經貼了網址，Claude 還先上網搜尋一輪。搜尋只回網址清單，讀內容本來就要靠 WebFetch。",
		before: "有網址 → WebSearch → WebFetch（2 步）",
		after:  "有網址 → WebFetch（1 步）",
		steps: []howStep{
			{"貼網址時加一句：「直接讀這個網址，不用搜尋」。", "Tools reference", docTools},
			{"本機的檔案用 <code>@檔名</code> 交給它，不要叫它去找。", "Best practices", docBest},
			{"常用文件放在哪，寫一行進 CLAUDE.md。", "Best practices", docBest},
		},
	},
}

// html renders the tutorial; extra is shown under extraTitle when the tag has one.
func (h howTo) html(extra string) string {
	var b strings.Builder
	b.WriteString(`<div class="howto"><div class="sec">怎麼省</div><p class="what">` + h.what + `</p>`)
	b.WriteString(`<div class="ba"><b class="no">改前</b><span>` + h.before + `</span><b class="ok">改後</b><span>` + h.after + `</span></div><ol>`)
	for _, s := range h.steps {
		b.WriteString(`<li>` + s.text)
		if s.url != "" {
			b.WriteString(` <a class="src" href="` + s.url + `" target="_blank" rel="noopener">官方：` + s.src + `</a>`)
		}
		b.WriteString(`</li>`)
	}
	b.WriteString(`</ol>`)
	if h.extraTitle != "" && extra != "" {
		b.WriteString(`<div class="extra-t">` + h.extraTitle + `</div>` + extra)
	}
	return b.String() + `</div>`
}
