# Mihomo Observer

針對一台 FlClash/Mihomo 的旁路觀測服務。每秒讀取 Controller 完整連線快照，保存 SQLite 歷史、生成小時與日統計，並在受保護的瀏覽器介面中展示觀測事實和問題證據。Observer 不判定分流錯誤、不驗證請求結果，也不修改 Mihomo 設定。

## 原生運行

需要 Go 1.25+。將 `config.example.yaml` 複製為 `config.yaml`，設定 Controller API、secret、獨立的 Web 密碼，然後執行：

```sh
go run ./cmd/mihomo-observer -config config.yaml
```

瀏覽器打開 `http://127.0.0.1:8080/`，以 HTTP Basic Auth 的 `admin` 和 `web.password` 登入。預設只監聽本機。Controller 若只監聽 `127.0.0.1:9090`，Observer 必須在同一台主機原生運行。

## Linux Docker

容器必須能到達 Controller，不能用容器內的 `127.0.0.1` 連到主機上的回環監聽。若 FlClash 只開放本機回環 Controller，請用原生運行。其他可達的 Linux Controller 可依下列步驟部署：

1. 複製 `config.docker.example.yaml` 為 `config.docker.yaml`，填入容器可達的 Controller 地址、Mihomo secret 和 Web 密碼。示例中的 `192.0.2.10` 是文檔佔位位址，必須替換。
2. 確認 `database.path` 為 `/data/observer.db`，`web.listen` 為 `0.0.0.0:8080`，配置文件可供容器內的 `observer` 用戶讀取。
3. 執行 `docker compose up -d --build`，並以 `curl -u admin:密碼 http://127.0.0.1:8080/api/dashboard` 核對資料。

Compose 將資料庫保存在 `observer_data` 命名卷，配置以唯讀檔案掛載；重建容器不會刪除卷。`docker compose down` 不會刪除卷；使用 `down -v` 會刪除歷史資料。映射埠預設只綁主機回環。若需要遠端瀏覽，請使用 TLS 反向代理或可信網路保護 Basic Auth；裸 HTTP 會暴露憑據。不要把 secret 寫進映像或公開日誌。

## Windows 原生運行

在 Linux/macOS 交叉編譯時執行 `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o mihomo-observer.exe ./cmd/mihomo-observer`；在 Windows PowerShell 原生編譯時執行 `$env:CGO_ENABLED="0"; go build -o mihomo-observer.exe ./cmd/mihomo-observer`。將 `config.example.yaml` 複製到執行檔同目錄，填入同機 FlClash Controller 的 `http://127.0.0.1:9090`、secret、Web 密碼，以及可寫的絕對資料庫路徑，例如 `C:\Users\你的使用者\AppData\Local\MihomoObserver\observer.db`；先建立資料夾。以 `mihomo-observer.exe -config config.yaml` 啟動。關閉視窗或按 Ctrl+C 後可再次啟動；同一資料庫會保留歷史。Controller 關閉或 secret 錯誤時，`/api/status` 的讀取錯誤數增加，Dashboard 會顯示目前連線數未知；日誌只列 HTTP 狀態或連線錯誤，不輸出 secret。

目前已完成 Windows amd64 交叉編譯；實際 Windows 啟動、退出、重啟和 FlClash 連線仍需在 Windows 主機上驗收。

## 介面與 API

所有頁面、靜態資源與 API 都使用相同的 Basic Auth。API 使用固定使用者名稱 `admin`，密碼為 `web.password`。

| 路徑 | 內容 |
| --- | --- |
| `/` | Dashboard、目標列表、問題證據與詳情 |
| `GET /api/status` | Collector 最近讀取/提交時間、錯誤數、丟幀數和核心版本 |
| `GET /api/dashboard` | 今日已觀測連線與全局流量、近期問題、採集缺口 |
| `GET /api/targets` | 有保留日統計的域名、IP-only 與目標 IP（最近 200 個） |
| `GET /api/problems` | 最近七天達到門檻的證據與檢查方向 |
| `GET /api/details/{kind}/{value}` | `domain`、`ip_only`、`destination_ip`、`asn`、`proxy_path` 的小時/日趨勢、路徑、有限原始樣本及相關證據 |
| `GET /api/history/{kind}/{value}?before_ms=...` | 以日期分頁的更早日統計；`before_ms` 為上一頁最早日期的毫秒時間戳 |
| `GET /api/problem-history/{kind}/{value}?start_ms=...&end_ms=...` | 指定時間範圍內保存的問題 occurrence 摘要，最多返回 1000 筆 |

首次快照只建立累計值基線；新鮮空快照顯示零連線，未採集或快照過期顯示未知。連線缺口後的首次累計值不補算；重啟時舊活動連線轉為 `unknown`。原始連線預設保留 14 天，小時統計 180 天，日統計永久；原始細節過期後，詳情仍可顯示留存的日統計。報表時區建庫後不可改。來源 IP、進程路徑、URL、載荷與 Authorization 不持久化。

最近讀取錯誤、寫入錯誤或佇列丟幀時，Dashboard 會立即將目前連線數顯示為未知；恢復後的缺口記錄包含原因，丟幀記錄包含數量。日歷史中的問題摘要讀取當時保存的 occurrence，不以後來的最新證據替換。

`analyzer.enabled: false` 只停用問題分析，小時/日統計仍會持續投影。新的目標或趨勢最多約 30 秒後出現在詳情頁；Dashboard 的連線與流量值直接讀取已提交快照。

目標列表與路徑詳情的跨日連線數是每日去重計數之和；同一長連線跨日或跨 Observer 重啟時，不能將該合計當作實際唯一連線數。

域名詳情的 `host`/`sniffHost` 來源摘要只使用仍在 Raw 保留期內的連線；Raw 到期後顯示未知，不從域名或 IP 猜測來源。

實際 Mihomo 可能不提供請求成功、精確結束時間、握手時長或應用協議。問題頁只提示短時重複連線與足夠樣本下的 IPv4/IPv6 差異；不表示重試失敗、協議回退或需要改成 DIRECT。詳見 [實作說明](docs/phase-6-implementation.md)與[票據及驗收狀態](docs/tracer-bullets.md)。
