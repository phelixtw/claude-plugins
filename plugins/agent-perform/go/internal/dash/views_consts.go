// Code generated from scripts/dash_views.py constants.

package dash

type adviceRule struct{ code, pat, label, text string }

var repeatAdvice = []adviceRule{
	{"1", "^(cd \\S+\\s*(&&|;)\\s*)?source\\b", "重載 shell 環境", "載入 shell 環境的指令：每次都得重跑（shell 狀態不會保留），輸出很小，可以不理。"},
	{"1", "^(cd \\S+\\s*(&&|;)\\s*)?open\\b", "重開同一個檔案", "又開一次同一個檔案給你看：只是開視窗，不佔 context，可以不理。"},
	{"1", "\\b(pytest|unittest|(npm|yarn|pnpm) (run )?test|cargo test|go test|flutter test|dotnet test|jest|vitest|rspec)\\b", "同一組測試重跑", "同一組測試又跑一次：只跑跟這次改動有關的測試，輸出只看失敗的部分（例如 -x -q，或接 tail）。"},
	{"1", "\\b(curl|lsof|ps|kubectl get|docker ps|podman ps)\\b|health", "反覆確認服務或呼叫同一個 API", "又確認一次同一個服務或程序：要等它好就改在背景跑，跑完會自動通知，不要反覆查。"},
	{"1", "\\b(git (status|diff|log|branch)|ls|pwd)\\b", "重複查 git／目錄狀態", "又查一次同樣的狀態：中間沒有改動的話，前一次的結果還在 context 裡，直接用。"},
	{"1", "\\b(build|make|gradlew?|xcodebuild)\\b", "同樣的建置重跑", "同樣的建置又跑一次：確定這次的改動都做完再建置，輸出只看錯誤。"},
	{"1", "", "其他指令重跑", "同一個指令又跑一次：前一次輸出太長看不到重點的話，改成只輸出需要的部分（grep、tail、--quiet）。"},
	{"2", "^Skill:", "同一個 skill 重複載入", "同一個 skill 又載入一次：skill 內容已經在 context 裡，照前面載入的內容做就好。"},
	{"2", "", "同一個 MCP 工具同參數重叫", "同一個 MCP 工具、同樣參數又叫一次：前一次的結果還在 context 裡，直接用；要跨對話用的就存成檔案。"},
	{"3", "://", "同一個網址重抓", "同一個網址又抓一次：第一次抓到的重點記下來，不要重抓。"},
	{"3", "\\|", "同樣的搜尋重跑", "同樣的搜尋又跑一次：前一次的結果還在 context 裡；要找別的就換關鍵字或縮小範圍。"},
	{"3", "", "沒改過的檔案重讀", "沒改過的檔案又讀一次：內容已經在 context 裡；只要其中一段時用 Read 指定行數範圍。"},
	{"4", "", "同一段內容改了好幾輪", "寫進去的內容跟前面高度相似，同一段改了好幾輪：動手前先把要改的地方列完，一次改好。"},
}
