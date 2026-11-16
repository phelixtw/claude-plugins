// Code generated from scripts/refs.py _SRCS; keep in sync by hand.

package refs

type source struct{ url, full, short, note string }

var order = []string{"ctx_eng", "ctx_sum", "cc_costs", "cc_delegate", "ctx_mgmt", "agentdiet", "multi", "subdoc", "thariq_rot", "dex_60", "cc_clear", "cc_cache_miss", "cc_compact", "cc_model", "cc_effort", "cc_mcp", "cc_claude_md", "cc_complex", "cc_specific", "cc_flags", "pricing", "cc_baseline"}

var srcs = map[string]source{
	"ctx_eng":       {"https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents", "Anthropic〈Effective context engineering for AI agents〉", "Anthropic context engineering", "：token 增加時回想能力下降，每個新 token 都消耗 attention budget；清掉舊的 tool result 是最輕量的壓縮"},
	"ctx_sum":       {"https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents", "Anthropic〈Effective context engineering for AI agents〉", "Anthropic context engineering", "：subagent 回傳濃縮摘要，常見 1,000–2,000 token"},
	"cc_costs":      {"https://code.claude.com/docs/en/costs#why-usage-climbs-in-a-long-session", "Claude Code 文件〈Manage costs effectively〉", "Claude Code 文件", "：長對話每次 API 呼叫都重送整段歷史；Cache 過期後的第一則訊息整段重寫"},
	"cc_delegate":   {"https://code.claude.com/docs/en/costs#delegate-verbose-operations-to-subagents", "Claude Code 文件〈Manage costs effectively：Delegate verbose operations to subagents〉", "Claude Code 文件", "：冗長的操作（跑測試、抓文件、處理 log）交給 subagent，冗長輸出留在它的 context，只有摘要回到主對話"},
	"ctx_mgmt":      {"https://claude.com/blog/context-management", "Anthropic〈Managing context on the Claude Developer Platform〉", "Anthropic 部落格", "：context editing 單用 +29%、搭配 memory tool +39%；100 輪 web search eval 的 token 用量 -84%"},
	"agentdiet":     {"https://arxiv.org/abs/2509.23586", "AgentDiet（arXiv 2509.23586）", "arXiv 2509.23586", "：tool 結果留在 trajectory 到結束、每步重算；移除無用、重複、過期內容後 input token 減少 39.9%–59.7%，效能不變"},
	"multi":         {"https://www.anthropic.com/engineering/multi-agent-research-system", "Anthropic〈How we built our multi-agent research system〉", "Anthropic 多 agent 研究", "：多 agent 系統約為 chat 的 15 倍 token（單 agent 約 4 倍）"},
	"subdoc":        {"https://code.claude.com/docs/zh-TW/sub-agents", "Claude Code 文件〈Subagents〉", "Claude Code 文件", "：subagent 從全新 context 開始，看不到主對話的內容和已讀檔案"},
	"thariq_rot":    {"https://x.com/trq212/article/2044548257058328723", "Thariq（Anthropic Claude Code 工程師）2026-04-16", "Thariq 2026-04-16", "：1M context 約在 300–400k 開始 context rot"},
	"dex_60":        {"https://youtu.be/YwZR6tc7qYg?t=1541", "Dex Horthy（HumanLayer）MLOps Community 演講 2026-03-24", "MLOps Community 2026-03-24", "：context 用量 40% 以下最安全，60% 準備收（HumanLayer 的建議，非 Anthropic）"},
	"cc_clear":      {"https://code.claude.com/docs/en/costs#manage-context-proactively", "Claude Code 文件〈Manage context proactively〉", "Claude Code 文件", "：換到無關工作用 /clear，舊 context 在之後每則訊息都浪費 token"},
	"cc_cache_miss": {"https://code.claude.com/docs/en/costs#why-usage-climbs-in-a-long-session", "Claude Code 文件〈Why usage climbs in a long session〉", "Claude Code 文件", "：休息超過 Cache 存活時間後的第一則訊息沒命中 Cache、整段重處理；訂閱 1 小時，用 usage credits 或 API key 預設 5 分鐘"},
	"cc_compact":    {"https://code.claude.com/docs/en/costs#why-usage-climbs-in-a-long-session", "Claude Code 文件〈Why usage climbs in a long session〉", "Claude Code 文件", "：/compact 會讀完整段要摘要的對話，本身是大請求；要重新開始時 /clear 不花錢"},
	"cc_model":      {"https://code.claude.com/docs/en/costs#choose-the-right-model", "Claude Code 文件〈Choose the right model〉", "Claude Code 文件", "：Sonnet 能處理多數寫程式工作且比 Opus 便宜；Opus 留給複雜架構決策與多步推理；簡單 subagent 用 haiku"},
	"cc_effort":     {"https://code.claude.com/docs/en/costs#adjust-extended-thinking", "Claude Code 文件〈Adjust extended thinking〉", "Claude Code 文件", "：thinking 以 output 計價；簡單工作用 /effort 調低"},
	"cc_mcp":        {"https://code.claude.com/docs/en/costs#reduce-mcp-server-overhead", "Claude Code 文件〈Reduce MCP server overhead〉", "Claude Code 文件", "：能用 CLI 就用 CLI；用 /mcp 關掉沒在用的 server"},
	"cc_claude_md":  {"https://code.claude.com/docs/en/costs#move-instructions-from-claude-md-to-skills", "Claude Code 文件〈Move instructions from CLAUDE.md to skills〉", "Claude Code 文件", "：CLAUDE.md 開場就載入；專用流程移到 skill 按需載入；CLAUDE.md 控制在 200 行內"},
	"cc_complex":    {"https://code.claude.com/docs/en/costs#work-efficiently-on-complex-tasks", "Claude Code 文件〈Work efficiently on complex tasks〉", "Claude Code 文件", "：複雜工作先用 plan mode；方向錯就按 Escape、用 /rewind；給驗證目標，讓 Claude 在你要求修正前先抓到問題"},
	"cc_specific":   {"https://code.claude.com/docs/en/costs#write-specific-prompts", "Claude Code 文件〈Write specific prompts〉", "Claude Code 文件", "：模糊的要求會觸發大範圍掃描；具體的要求讀最少的檔案"},
	"cc_flags":      {"https://code.claude.com/docs/en/costs#plan-usage-breakdown", "Claude Code 文件〈Plan usage breakdown〉", "Claude Code 文件", "：/usage 在某行為（long context、cache misses）佔近期用量 10% 以上時標出"},
	"pricing":       {"https://platform.claude.com/docs/en/about-claude/pricing", "Anthropic 模型價目表（Pricing）", "Anthropic 價目表", "：全站「花費」＝token × 這張價目（程式在 go/internal/prices）；cache 讀多數模型為輸入價的 0.1 倍（Opus 5.5、Sonnet 5.5 為 0.05 倍，Fable 5.1 為 0.025 倍），cache 寫 5 分鐘為 1.25 倍、1 小時為 2 倍"},
	"cc_baseline":   {"https://code.claude.com/docs/en/costs", "Claude Code 文件〈Manage costs effectively〉", "Claude Code 文件", "：先用追蹤工具建立基準，再擴大使用"},
}
