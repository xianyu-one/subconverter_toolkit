# 部署與編譯

本頁使用倉庫中的範例配置。每個 Observer 只連接一個可達的 Mihomo Controller，並使用一份獨立的 SQLite 資料庫。先設定 `mihomo.api`、`mihomo.secret` 與 `web.password`；資料庫所在目錄必須可寫。配置內含憑據，部署時限制檔案讀取權限。

## Make 構建

在 `mihomo-observer/` 目錄執行：

```sh
make build                 # 當前平台，輸出 dist/mihomo-observer
make test                  # Go 測試
make vet                   # Go 靜態檢查
make docker                # 本機 Docker 鏡像 mihomo-observer:local
make cross                 # 常用 Linux/OpenWrt 與 Windows 二進位檔
```

可單獨指定 `linux-amd64`、`linux-arm64`、`linux-armv7`、`windows-amd64`、`windows-arm64`。所有二進位檔輸出至 `dist/`，使用 `CGO_ENABLED=0`，不依賴目標機安裝 Go。ARMv7 使用 `GOARM=7`。OpenWrt 有多種 CPU 架構，請先核對設備架構和可用儲存空間，再選擇對應檔案。當前 SQLite 依賴無法編譯 MIPS/MIPSLE，這些 OpenWrt 設備暫無本專案二進位檔；本倉庫也沒有為每種 OpenWrt 目標建立安裝包。

自訂鏡像名稱或建置輸出位置：

```sh
make docker IMAGE=example/mihomo-observer:dev
make linux-arm64 DIST=/tmp/observer-build
```

CI 使用 `make docker-multi` 驗證 `linux/amd64,linux/arm64`，在 `main` 推送後使用 `make docker-push` 發布。手動多平台發布需先建立 Buildx builder 並登入鏡像倉庫，再設定 `IMAGE_TAGS` 和 `PLATFORMS`；`docker-push` 會實際推送。

## 配置檔路徑

程式依序使用顯式 `-config`、環境變數 `MIHOMO_OBSERVER_CONFIG`、預設 `config.yaml`。環境變數是**程式所在環境可讀到的配置檔路徑**；它不會自動把宿主機檔案掛入容器。修改配置後重啟服務。

```sh
MIHOMO_OBSERVER_CONFIG=/etc/mihomo-observer/config.yaml ./dist/mihomo-observer
./dist/mihomo-observer -config /other/config.yaml
```

## Docker Compose

先將 `config.docker.example.yaml` 複製為 `config.docker.yaml` 並修改。默認 Compose 把它掛入容器的 `/etc/observer/config.yaml`，使用命名卷保存 `/data/observer.db`：

```sh
docker compose up -d --build
curl -u admin:你的密碼 http://127.0.0.1:8080/api/dashboard
```

可分別用 `OBSERVER_CONFIG_FILE` 指定**宿主機**檔案，用 `MIHOMO_OBSERVER_CONFIG` 指定**容器內**路徑；Compose 會將前者掛載到後者，並將後者傳給程式：

```sh
OBSERVER_CONFIG_FILE=/srv/observer/site.yaml \
MIHOMO_OBSERVER_CONFIG=/run/observer/site.yaml \
docker compose up -d --build
```

直接使用鏡像時同樣需同時掛載檔案與傳入容器內路徑：

```sh
docker run -d --name mihomo-observer -p 127.0.0.1:8080:8080 \
  -e MIHOMO_OBSERVER_CONFIG=/run/observer/site.yaml \
  -v /srv/observer/site.yaml:/run/observer/site.yaml:ro \
  -v observer_data:/data mihomo-observer:local
```

容器內的 `127.0.0.1` 不是宿主機；配置中的 `mihomo.api` 必須由容器連得上。若 Controller 僅在普通客戶端的回環位址監聽，使用下方原生部署。容器以非 root 的 `observer` 用戶運行，宿主機配置檔必須可供該用戶讀取。對外提供 Web 頁面時使用 TLS 反向代理或可信私有網路，避免 Basic Auth 憑據經裸 HTTP 傳送。

## 普通 Linux 客戶端：systemd

先執行 `make linux-amd64` 或設備對應的 Linux 目標。以 amd64 為例，在專案目錄安裝二進位檔、服務單元與配置樣本：

```sh
sudo useradd --system --home-dir /var/lib/mihomo-observer --shell /usr/sbin/nologin mihomo-observer
sudo install -m 0755 dist/mihomo-observer-linux-amd64 /usr/local/bin/mihomo-observer
sudo install -d -m 0750 /etc/mihomo-observer
sudo install -m 0640 -o root -g mihomo-observer deploy/systemd/config.example.yaml /etc/mihomo-observer/config.yaml
sudo install -m 0644 deploy/systemd/mihomo-observer.service /etc/systemd/system/mihomo-observer.service
sudoedit /etc/mihomo-observer/config.yaml
sudo systemctl daemon-reload
sudo systemctl enable --now mihomo-observer
```

服務使用 `MIHOMO_OBSERVER_CONFIG=/etc/mihomo-observer/config.yaml`，由 systemd 建立可寫的 `/var/lib/mihomo-observer`，資料庫保存在該目錄。狀態與日誌：

```sh
systemctl status mihomo-observer
journalctl -u mihomo-observer -n 100 --no-pager
curl -u admin:你的密碼 http://127.0.0.1:8080/api/dashboard
```

修改配置後執行 `sudo systemctl restart mihomo-observer`。如需不同配置路徑，使用 `sudo systemctl edit mihomo-observer` 覆寫 `Environment=MIHOMO_OBSERVER_CONFIG=...`，並確保服務用戶可讀配置。不要把 Web 密碼或 Controller secret 寫在 unit 的環境變數中。

## OpenWrt：procd init

先確認路由器上有可連接的 Controller，並核對 CPU 架構。下例假設使用 `linux-arm64`，其他設備請換成對應的 `dist/` 檔案。建議將 SQLite 資料庫放在持久掛載的磁碟；範例使用 `/mnt/mihomo-observer/observer.db`，啟動前應確保該掛載和目錄已就緒，避免把大量寫入放在容量有限的路由器快閃儲存中。

```sh
make linux-arm64
ssh root@路由器 'mkdir -p /etc/mihomo-observer /mnt/mihomo-observer && chmod 700 /mnt/mihomo-observer'
scp dist/mihomo-observer-linux-arm64 root@路由器:/usr/bin/mihomo-observer
scp deploy/openwrt/mihomo-observer.init root@路由器:/etc/init.d/mihomo-observer
scp deploy/openwrt/mihomo-observer.uci root@路由器:/etc/config/mihomo-observer
scp deploy/openwrt/config.example.yaml root@路由器:/etc/mihomo-observer/config.yaml
```

登入路由器，編輯 `/etc/mihomo-observer/config.yaml` 的 API、secret、Web 密碼與資料庫路徑，再啟用服務：

```sh
chmod 755 /usr/bin/mihomo-observer /etc/init.d/mihomo-observer
chmod 600 /etc/mihomo-observer/config.yaml
/etc/init.d/mihomo-observer enable
/etc/init.d/mihomo-observer start
/etc/init.d/mihomo-observer status
logread -e mihomo-observer
```

Init 腳本使用 OpenWrt 的 procd 管理前台程式、自動重啟與日誌，並從 `/etc/config/mihomo-observer` 取得配置檔路徑，再透過 `MIHOMO_OBSERVER_CONFIG` 傳給程式。要變更路徑，可編輯該 UCI 檔案或執行 `uci set mihomo-observer.main.config_file='/新路徑/config.yaml'; uci commit mihomo-observer`，然後執行 `/etc/init.d/mihomo-observer restart`。修改 YAML 也需要重啟；不必重建二進位檔。範例 Web 只監聽路由器回環位址，可從本機透過 SSH 通道瀏覽：`ssh -L 8080:127.0.0.1:8080 root@路由器`，再開啟 `http://127.0.0.1:8080/`。

OpenWrt init/procd 介面與服務參數參考 [OpenWrt procd init scripts](https://openwrt.org/docs/guide-developer/procd-init-scripts) 和 [OpenWrt procd.sh](https://git.openwrt.org/openwrt/openwrt/tree/?path=package%2Fsystem%2Fprocd%2Ffiles%2Fprocd.sh)；systemd 的資料目錄與重啟行為參考 [systemd.exec](https://github.com/systemd/systemd/blob/main/man/systemd.exec.xml) 和 [systemd.service](https://github.com/systemd/systemd/blob/main/man/systemd.service.xml)。
