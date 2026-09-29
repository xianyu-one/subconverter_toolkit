# GeoX Sync

定时下载 GeoIP、GeoSite、国家 MMDB 和 ASN MMDB 到本地目录。启动后立即同步，以后默认每 24 小时同步一次。每个文件单独更新：下载成功且检查通过后替换，失败时保留旧文件。

## 本地构建与运行

```sh
make build
./dist/geox-sync --once --output-dir ./data
./dist/geox-sync --interval 12h --proxy 'socks5h://user:password@127.0.0.1:1080'
```

`make test` 和 `make vet` 分别运行测试与静态检查。`--once` 中任一已启用文件失败时退出码非零；定时模式会记录错误并在下一轮重试。`socks5` 和 `socks5h` 都由代理端解析上游域名。代理连接失败不会改为直连。代理凭据不会写入日志，但命令行参数可能被本机其他进程看到；有凭据时建议使用环境变量。

构建容器时可通过 `make docker GOPROXY=https://goproxy.cn` 覆盖 Go 模块下载源；默认使用 `https://proxy.golang.org`。

## 容器

```sh
docker run -d --name geox-sync --restart unless-stopped \
  -v "$PWD/data:/data" \
  -e GEOX_SYNC_PROXY='socks5h://user:password@host.docker.internal:1080' \
  mrxianyu/geox-sync:latest
```

容器以非 root 用户运行；绑定的 `/data` 目录必须允许容器用户写入。也可使用 Docker 命名卷。镜像支持 `linux/amd64` 和 `linux/arm64`。相关 PR 会验证构建；推送到 `main` 或手动运行 GitHub Actions 时，发布 `latest` 与 `sha-<提交 SHA>` 标签。

## 配置

命令行参数优先于环境变量，环境变量优先于默认值。布尔参数可写成 `--geoip-enabled=false`。

| 参数 | 环境变量 | 默认值 |
| --- | --- | --- |
| `--output-dir` | `GEOX_SYNC_OUTPUT_DIR` | 本地 `./data`；容器 `/data` |
| `--interval` | `GEOX_SYNC_INTERVAL` | `24h`，上一轮完成后计时 |
| `--timeout` | `GEOX_SYNC_TIMEOUT` | `2m`，每个下载的总超时 |
| `--max-bytes` | `GEOX_SYNC_MAX_BYTES` | `536870912`（512 MiB） |
| `--proxy` | `GEOX_SYNC_PROXY` | 空，直连；支持 `http`、`https`、`socks5`、`socks5h` URL 及 URL 用户名密码 |
| `--once` | `GEOX_SYNC_ONCE` | `false` |

| 数据 | 源参数 / 环境变量 | 启用参数 / 环境变量 | 默认源 | 输出文件 |
| --- | --- | --- | --- | --- |
| GeoIP | `--geoip-url` / `GEOX_SYNC_GEOIP_URL` | `--geoip-enabled` / `GEOX_SYNC_GEOIP_ENABLED` | [geoip.dat](https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geoip.dat) | `geoip.dat` |
| GeoSite | `--geosite-url` / `GEOX_SYNC_GEOSITE_URL` | `--geosite-enabled` / `GEOX_SYNC_GEOSITE_ENABLED` | [geosite.dat](https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geosite.dat) | `geosite.dat` |
| 国家 MMDB | `--mmdb-url` / `GEOX_SYNC_MMDB_URL` | `--mmdb-enabled` / `GEOX_SYNC_MMDB_ENABLED` | [country.mmdb](https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/country.mmdb) | `country.mmdb` |
| ASN MMDB | `--asn-url` / `GEOX_SYNC_ASN_URL` | `--asn-enabled` / `GEOX_SYNC_ASN_ENABLED` | [GeoLite2-ASN.mmdb](https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/GeoLite2-ASN.mmdb) | `GeoLite2-ASN.mmdb` |

URL 可指向 HTTP 或 HTTPS 上游。HTTPS 源的重定向必须保持 HTTPS。下载要求 HTTP 200、内容非空且不超大小上限；MMDB 文件还会在替换前解析检查。默认不使用系统的 `HTTP_PROXY` / `HTTPS_PROXY` 环境变量，代理请通过 `GEOX_SYNC_PROXY` 或 `--proxy` 指定。
