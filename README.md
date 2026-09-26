# Subconverter Toolkit

一套围绕 [Subconverter](https://github.com/tindy2013/subconverter) 和 [Mihomo](https://github.com/MetaCubeX/mihomo) 构建的工具集，包含自定义转换镜像、Clash/Mihomo 配置模板、规则集、Include 检查工具、订阅预取代理和连接观测器。

本项目主要面向自建服务。请妥善保护订阅地址、鉴权令牌和私有节点文件，不要将包含敏感信息的配置提交到公开仓库。

## 功能概览

- 构建预置自定义模板的 Subconverter 镜像
- 为 Clash/Mihomo 生成经过 DNS、Fake-IP 和策略组优化的配置
- 提供在线规则版和私有部署版转换配置
- 维护自用的直连与解禁规则列表
- 检查 Include 文件的格式、换行和重复域名
- 通过前置节点获取需要二次请求的真实订阅
- 从订阅节点中提取域名，追加到 Rule List 和 Fake-IP Filter
- 向转换请求中按需注入私有节点，并生成链式代理配置
- 采集 Mihomo Controller 的连接快照，查看目标、路径、问题和历史趋势

## 项目结构

| 路径 | 用途 |
| --- | --- |
| `subconverter/` | 自定义 Subconverter 镜像、转换 INI、服务端模板和 Include 检查工具 |
| `subconverter/all-online.ini` | 仅依赖在线规则的转换配置 |
| `subconverter/lite-online.ini` | 在线规则的链式代理配置，直连例外之外统一使用私有出口 |
| `rule-list/` | 项目维护的规则列表 |
| `prefetch-proxy/` | Subconverter 前置代理，用于二次订阅、域名提取和私有节点注入 |
| `mihomo-observer/` | Mihomo 连接快照采集、SQLite 历史分析与受保护的浏览器界面 |
| `tests/` | 跨组件配置契约测试 |

## 自定义 Subconverter 镜像

`subconverter/Dockerfile` 基于 `ghcr.io/metacubex/subconverter:latest`，将以下内容写入镜像：

- `subconverter/server_conf/pref.toml`：Subconverter 服务端配置
- `subconverter/server_conf/all_base.tpl`：各客户端的基础配置模板
- `subconverter/server_conf/emoji.toml`：节点名称 Emoji 规则
- `subconverter/server_conf/include/`：Clash/Mihomo 的 Fake-IP Filter 内容
- `subconverter/all-online.ini`、`subconverter/lite-online.ini`：镜像内的转换配置，分别位于 `config/all-online.ini`、`config/lite-online.ini`

构建命令：`docker build -t subconverter:local ./subconverter`。私有部署若需要 `new.ini`，请自行准备并挂载为容器内的 `/base/config/new.ini`；仓库没有该文件，公开镜像不会打包它。

`subconverter/` 中的 Dockerfile、INI、服务端配置或对应工作流发生变更并推送到 `main` 时，GitHub Actions 构建 `linux/amd64`、`linux/arm64` 镜像并发布为：

```text
mrxianyu/subconverter:latest
mrxianyu/subconverter:<上游 Subconverter 版本>
```

相关 PR 仅构建验证，不发布；也可手动运行工作流。月度定时重建已移除，避免仓库内容没有相关变更时重复构建。

### 直接运行

```yaml
services:
  subconverter:
    image: mrxianyu/subconverter:latest
    container_name: subconverter
    restart: unless-stopped
    ports:
      - "25500:25500"
```

启动服务：

```bash
docker compose up -d
```

随后按标准 Subconverter 接口发起转换请求：

```text
http://localhost:25500/sub?target=clash&url=<URL 编码后的订阅地址>&config=<URL 编码后的 INI 地址>
```

如果服务暴露在公网，请在反向代理层增加访问控制，避免订阅地址被第三方读取或滥用。

## 转换配置

### `all-online.ini`

通用的在线规则版本。它会生成按地区和业务划分的策略组，并引用 ACL4SSR 与本仓库公开的规则列表，适合能够直接访问 GitHub Raw 内容的 Subconverter 实例。

使用时，将文件的 Raw URL 作为 Subconverter 的 `config` 参数：

```text
https://raw.githubusercontent.com/xianyu-one/subconverter-toolkit/main/subconverter/all-online.ini
```

示例：

```text
https://your-subconverter.example/sub?target=clash&url=<订阅地址>&config=https%3A%2F%2Fraw.githubusercontent.com%2Fxianyu-one%2Fsubconverter-toolkit%2Fmain%2Fsubconverter%2Fall-online.ini
```

### `lite-online.ini`

链式代理专用的在线规则版本。它沿用 `all-online.ini` 的 `DIRECT` 列表；未命中直连规则的流量进入 `🛫 PASSWALL`，再由 `🔰 节点选择` 选择手动、延迟最低、故障切换或 `🔒 私有出口选择`。私有出口节点需由 Prefetch Proxy 通过 `chaintoken` 注入，并以 `dialer-proxy: 🚀 前置节点池` 连接前置节点。使用时将以下 Raw URL 作为 `config` 参数；自建镜像也可使用 `config/lite-online.ini`：

```text
https://raw.githubusercontent.com/xianyu-one/subconverter-toolkit/main/subconverter/lite-online.ini
```

原先指向仓库根目录的两个 Raw URL 在目录迁移后需要改为以上路径；已有固定订阅地址也应同步更新。

### Mihomo Redir-Host + TUN

Clash 目标默认保留原有 Fake-IP 配置；在转换 URL 上添加 `clash.redir-host=1`，即可生成 Redir-Host + TUN + Sniffer 配置。该参数会直接启用 TUN，无须再加 `clash.tun-set=1`。原有 `clash.tun-set=1` 仍可单独启用 Fake-IP + TUN。

```text
https://your-prefetch-proxy.example/sub?target=clash&url=<订阅地址>&config=<new.ini 地址>&chaintoken=<令牌>&clash.redir-host=1
```

Redir-Host 返回真实 IP，允许 Android 私人 DNS 和浏览器安全 DNS 继续使用。进入 TUN 的普通 UDP/TCP 53 由内部 DNS 接管；客户端自己的 DoH/DoT/DoQ 是普通网络连接，不会被 `dns-hijack` 解密或替换。模板通过加密 DNS 解析节点域名，普通解析器通过当前 `🔰 节点选择` 策略组连接。为避免引导环路，节点域名的首次解析会以加密 DoH 直达固定解析器。直连流量使用单独的加密 DNS。

`all-online.ini` 在原分流规则之前，加入了常见加密 DNS 解析器地址及 DoT/DoQ、STUN/TURN 常用端口的优先代理规则。它们跟随 `🔰 节点选择`，因此该组应选中预期的链式出口，不要选中 `DIRECT`。这份示例列表无法识别所有使用 HTTPS/443 的自定义 DoH 或所有 WebRTC 服务；请把实际使用的解析器域名和 IP 补进配置。Sniffer 用 HTTP/TLS/QUIC 中可见的域名辅助匹配规则，保留连接的原始目标 IP；关闭 Sniffer 后，普通 DNS、IP 连接与 `dialer-proxy` 建链仍可工作，部分只有 IP 的连接会失去域名规则匹配。

检查 Redir-Host 时，请看**原始订阅 YAML** 的 `dns.enhanced-mode`，并确认没有输出 `fake-ip-range`、`fake-ip-filter` 等键。FlClash 展开的运行配置可能显示 Mihomo 的默认 DNS 字段，包括未启用的 Fake-IP 默认值；这些字段本身不表示 DNS 正在使用 Fake-IP。若导入时报“缺少前置代理组”，请在原始订阅 YAML 的 `proxy-groups` 中确认存在与节点 `dialer-proxy` 完全同名的 `🚀 前置节点池`；若缺失，检查本次转换实际加载的 `config` URL 是否指向包含该组的 INI，以及 Subconverter 是否成功取得该文件。仓库中的两份 INI 均应包含这个组。

自建 Subconverter 镜像可将固定订阅配置的 `params.config` 设为 `config/all-online.ini` 或 `config/lite-online.ini`，直接读取镜像内文件。若使用自行维护的 `new.ini`，先将它挂载到容器内的 `config/new.ini`。改动已打包的 INI 后需要重新构建并部署镜像。若要代取其中的 HTTP(S) 规则集，还需把同一份 INI 以只读方式挂载到 Prefetch Proxy，并设置 `CONFIG_DIR`（见下文）。外部 HTTPS 配置会由 Prefetch Proxy 读取；请确保它能访问该地址。

例如自行准备并挂载 `new.ini` 后，可将固定订阅设为：

```yaml
params:
  target: clash
  config: config/new.ini
  clash.redir-host: '1'
```

启用前逐个平台检查运行中的最终配置：

| 平台 | 检查项 |
| --- | --- |
| Nikki / OpenWrt | 检查其 UCI 覆写后的 DNS 模式、TUN、Sniffer、IPv4/IPv6 转发、LAN 接管与本模板一致。只对经旁路由的设备设置网关与 DNS；不要对整个局域网设置强制 DNS 劫持。 |
| OpenClash / OpenWrt | 选择 `redir-host-tun` 运行模式，检查插件对 DNS、TUN、IPv6、Sniffer 的覆写。核实网关设备的防火墙和 IPv6 转发规则。 |
| FlClash / Android | 启用 VPN/TUN 并核对应用覆盖范围；私人 DNS 的加密连接须进入 VPN。使用 Android 的常驻 VPN 和“阻止无 VPN 连接”处理客户端停止后的流量。 |
| FlClash / Windows、Linux | 核对 TUN 实际启用和 DNS/路由覆写；Windows 使用严格路由，Linux 检查策略路由。若需要客户端停止后断网，须另设系统防火墙规则。 |

IPv6 应在每个平台验证确实经代理出口；节点或客户端无法可靠转发 IPv6 时，应在该设备上阻断 IPv6，不能只关闭 DNS AAAA 回答。WebRTC 的网络出口可经 TUN 和优先规则约束，但浏览器暴露本地候选地址的行为仍需浏览器自身设置。建议实际测试 A/AAAA、UDP/TCP 53、所用 DoH/DoT/DoQ、WebRTC、节点故障与 TUN 退出后是否断网。详情及官方文档链接见[设计说明](subconverter/docs/mihomo-redir-host-tun-design.md)。

## 规则列表

`rule-list/` 中的文件使用 Subconverter/Clash Rule Provider 可识别的文本规则格式。目前主要用于补充直连、国内云服务、数据库软件、Plex 和解禁相关域名。

在转换配置中引用规则文件：

```ini
ruleset=DIRECT,https://raw.githubusercontent.com/xianyu-one/subconverter-toolkit/main/rule-list/remote.list
```

新增规则时应保持一行一条规则，例如：

```text
DOMAIN-SUFFIX,example.com
DOMAIN,api.example.net
```

提交前建议同时检查引用该文件的 INI 配置，避免文件改名后留下失效链接。

## Include 文件检查工具

`subconverter/check_include.py` 用来检查 `subconverter/server_conf/include/` 一类 YAML 列表文件。它仅依赖 Python 3.9+ 标准库，会扫描所选目录第一层的所有 `.txt` 文件并检查：

- LF、CRLF 和孤立 CR 换行
- 文件末尾是否存在换行符
- UTF-8 BOM、空白行和 Tab 缩进
- 不符合 `- domain` 形式的列表项
- 单个文件内及多个文件之间的重复域名

运行：

```bash
python3 subconverter/check_include.py
```

按照提示输入目录；在支持 GNU Readline 的环境中可以使用 Tab 补全：

```text
subconverter/server_conf/include
```

检查结果会显示在终端，并在被检查目录生成带时间戳的完整报告：

```text
include_check_YYYYMMDD_HHMMSS.report
```

检查默认只读。如果发现文件末尾缺少 LF，程序会询问是否修复；确认后会先在原目录创建 `.bak.<时间戳>` 备份，再追加换行符。工具不会自动删除重复规则或修复其他格式问题。

## Prefetch Proxy

`prefetch-proxy/` 是位于客户端与 Subconverter 之间的 Go 反向代理。普通请求会直接转发；命中指定订阅域名时，它会启动临时 Mihomo，通过订阅提供的前置节点再次获取真实订阅，再把缓存地址交给 Subconverter。

请求流程：

```text
客户端
  -> Prefetch Proxy
       -> 普通订阅：直接交给 Subconverter
       -> 特殊订阅：获取前置节点 -> 启动临时 Mihomo -> 获取真实订阅
       -> 可选：提取节点域名并更新规则文件
       -> 可选：注入私有节点
       -> 可选：读取自定义 INI，改写 HTTP(S) ruleset，并代取规则文件
  -> Subconverter
  -> 转换结果
```

### 构建镜像

```bash
docker build -t prefetch-proxy:latest ./prefetch-proxy
```

当前 `prefetch-proxy/Dockerfile` 内置的是 Mihomo `v1.18.2` 的 `linux-amd64` 二进制，因此该镜像当前只适合 AMD64 环境。若要部署到 ARM64，需要调整 Mihomo 下载目标。

仓库的 `Prefetch Proxy Docker Build` 工作流会在相关 PR 上验证镜像构建；`prefetch-proxy/` 中的 Dockerfile、Go 源码与依赖或对应工作流的变更进入 `main`，或手动触发时，会使用现有的 `DOCKERHUB_USERNAME` 与 `DOCKERHUB_TOKEN` 仓库密钥发布 `mrxianyu/prefetch-proxy:latest` 和对应的 `sha-<提交 SHA>` 标签。该工作流仅构建 `linux/amd64`。

### 与 Subconverter 一起运行

下面的示例启用二次订阅处理，并将服务统一暴露在宿主机的 `25500` 端口：

```yaml
services:
  prefetch-proxy:
    build: ./prefetch-proxy
    image: prefetch-proxy:latest
    ports:
      - "25500:8080"
    environment:
      LISTEN_ADDR: ":8080"
      SUBCONVERTER_URL: "http://subconverter:25500"
      INTERNAL_BASE_URL: "http://prefetch-proxy:8080"
      TARGET_DOMAINS: "special-provider.example,another-provider.example"
      PROXY_PORT: "28080"
      API_PORT: "9090"
      DEBUG: "false"
    depends_on:
      - subconverter
    restart: unless-stopped

  subconverter:
    image: mrxianyu/subconverter:latest
    restart: unless-stopped
```

启动：

```bash
docker compose up -d --build
```

客户端继续使用标准 Subconverter 路径，只需将服务地址改为 Prefetch Proxy：

```text
http://localhost:25500/sub?target=clash&url=<URL 编码后的订阅地址>&config=<INI 地址>
```

`url` 参数包含多个以 `|` 分隔的订阅时，代理会逐一判断。仅命中 `TARGET_DOMAINS` 的订阅会进入二次获取流程；其他订阅直接交给 Subconverter。

二次获取的结果缓存在内存中一小时。服务重启后缓存会丢失，同一订阅的下一次请求将重新执行预取。

### 自动更新节点域名规则

设置 `RULE_LIST_PATH` 或 `FAKE_IP_FILTER_PATH` 后，Prefetch Proxy 会把订阅转换为 Clash YAML，从每个节点的 `server` 字段提取非 IP 地址，并去重追加到指定文件：

- Rule List 写入 `DOMAIN-SUFFIX,<域名>`
- Fake-IP Filter 写入 `- +.<域名>`，并尝试沿用文件已有缩进

Compose 配置示例：

```yaml
services:
  prefetch-proxy:
    # 省略其他配置
    environment:
      RULE_LIST_PATH: "/data/subscription-domains.list"
      FAKE_IP_FILTER_PATH: "/data/subscription-domains.txt"
    volumes:
      - ./rules-data:/data

  subconverter:
    # 如果模板或规则需要读取这些文件，应挂载同一个目录
    volumes:
      - ./rules-data:/data
```

该功能同时处理普通订阅和二次订阅。相同的普通订阅组合一小时内只会预解析一次。目标文件不存在时程序会创建它，但挂载目录必须已经存在且可写。

### 注入私有节点与链式代理

将私有节点、组和密钥保存在同一份 YAML 中。`proxies` 沿用 Clash 节点字段，`groups` 仅用于选择节点，不会传给 Subconverter：

```yaml
proxies:
  - name: private-node
    groups: [family, shared]
    type: ss
    server: private.example.com
    port: 443
    cipher: aes-128-gcm
    password: change-me
keys:
  - name: family
    token: replace-with-a-long-random-token
    nodes: [private-node]
    groups: [shared]
```

配置 Prefetch Proxy：

```yaml
services:
  prefetch-proxy:
    # 省略其他配置
    environment:
      PRIVATE_CONFIG_PATH: "/run/secrets/private-config.yaml"
    volumes:
      - ./private-config.yaml:/run/secrets/private-config.yaml:ro
```

请求时添加令牌：

```text
http://localhost:25500/sub?target=clash&url=<订阅地址>&config=<new.ini 地址>&chaintoken=<对应的 token>
```

令牌匹配时，代理会把该密钥通过节点名称与组选择的节点作为额外订阅交给 Subconverter；名称与组的选择结果取并集，按节点定义顺序去重，并执行以下处理：

- 节点名称增加 `🔒私有 - ` 前缀
- 节点增加 `dialer-proxy: 🚀 前置节点池`
- `new.ini` 将私有节点加入 `🔒 私有出口选择`

私有节点通过每次请求生成的随机内部地址提供，地址只允许读取该密钥选中的节点，10 分钟后失效；固定的 `/internal/private` 已移除。缺少 `chaintoken` 时正常转换，错误令牌返回 HTTP 403。令牌不会转发给 Subconverter。配置仅在启动时读取，修改后须重启；未设置 `PRIVATE_CONFIG_PATH` 时，私有节点功能关闭。

请将含真实令牌和节点密码的配置文件保留在仓库外，并以只读卷挂载；部署前按上面的示例核对配置格式。

### 固定订阅配置

客户端可以保存固定的 `/sub` 地址，把上游订阅和 Subconverter 参数放在 Prefetch Proxy 的配置文件中。一个密钥可有多份配置；每份配置引用本密钥下的若干命名上游。修改文件并重启 Prefetch Proxy 后，客户端地址保持不变。

先在 `PRIVATE_CONFIG_PATH` 的文件中定义密钥。只使用固定订阅时，可以省略 `proxies`、`nodes` 和 `groups`：

```yaml
keys:
  - name: alice
    token: replace-with-a-long-random-token
```

再创建固定订阅配置文件，例如 `/run/secrets/cover-profiles.yaml`：

```yaml
keys:
  - name: alice
    upstreams:
      first: https://provider-1.example/subscription/example
      second: https://provider-2.example/subscription/example
      third: https://provider-3.example/subscription/example
    profiles:
      "1":
        upstreams: [first, second]
        params:
          target: clash
          config: config/all-online.ini
          filename: abc
      "2":
        upstreams: [first, second, third]
        params:
          target: clash
          include: '(香港|日本)'
          exclude: '(到期|剩余流量)'
```

设置 `COVER_PROFILE_CONFIG_PATH=/run/secrets/cover-profiles.yaml`，并将两个文件以只读卷挂载。客户端使用：

```text
https://your-prefetch-proxy.example/sub?chaintoken=<令牌>&coverprofile=1
```

`params.include` 和 `params.exclude` 都是匹配**节点名称**的正则表达式字符串，分别表示只保留匹配的节点、排除匹配的节点。YAML 中直接写原始表达式，建议用单引号包住；不要预先做 URL 编码，也不要写成 YAML 列表。比如 `include: '(香港|日本)'` 表示名称含“香港”或“日本”，`exclude: '(到期|剩余流量)'` 表示排除名称含任一关键词的节点。如果名称必须同时含“香港”和“专线”，可写 `include: '(?=.*香港)(?=.*专线)'`，与两个词的先后顺序无关。正则里的 `|` 是“或”，不是上游订阅 `url` 的分隔符。

文件中的表达式由 Prefetch Proxy 作为查询参数转发时自动 URL 编码；只有手工拼接 `/sub?...&include=...` 请求地址时，才需要对参数值做 URL 编码。`include`、`exclude` 各写一个字符串；需要多个备选关键词时在同一表达式中用 `|` 组合。它们与 `pref.toml` 中的 `include_remarks`、`exclude_remarks` 默认设置不同：这里的值属于单份固定订阅配置，并会作为请求参数覆盖 Subconverter 的对应默认值。

请求中的 `url` 会整体替换配置中的上游列表；`target`、`config`、`exclude`、`include`、`filename` 等参数逐项覆盖文件默认值。显式空 `exclude=` 可清除文件值，空 `url=` 会报错。不带 `coverprofile` 的旧 `/sub` 请求继续使用客户端提供的参数。

多个上游中只要有一个提供可解析节点即可继续转换；全部失败时返回错误，私有节点不计入成功上游。代理会跳过预取失败的上游；普通上游的部分失败依赖后端 Subconverter 启用 `skip_failed_links = true`，本仓库提供的 `pref.toml` 已启用。完整格式及错误行为见[固定订阅配置设计](prefetch-proxy/docs/prefetch-proxy-cover-profiles-design.md)。

### 环境变量

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `LISTEN_ADDR` | `:8080` | Prefetch Proxy 监听地址 |
| `SUBCONVERTER_URL` | `http://subconverter:25500` | 后端 Subconverter 地址 |
| `TARGET_DOMAINS` | 程序默认为空；镜像预设 `special-provider.com` | 需要二次获取的订阅域名，多个值用英文逗号分隔；部署时应显式设置 |
| `MIHOMO_PATH` | `/usr/local/bin/mihomo` | Mihomo 可执行文件路径 |
| `PROXY_PORT` | `28080` | 临时 Mihomo SOCKS5 端口 |
| `API_PORT` | `9090` | 临时 Mihomo Controller 端口 |
| `INTERNAL_BASE_URL` | `http://prefetch-proxy:8080` | Subconverter 用来回读缓存和私有节点的容器内地址 |
| `CONFIG_DIR` | 空 | 可选的 INI 共享目录根路径；设置后代理会读取此目录下的相对 `config` 路径 |
| `RULE_LIST_PATH` | 空 | 节点域名 Rule List 的写入路径；为空时禁用 |
| `FAKE_IP_FILTER_PATH` | 空 | 节点域名 Fake-IP Filter 的写入路径；为空时禁用 |
| `PRIVATE_CONFIG_PATH` | 空 | 私有节点、组和密钥 YAML 路径；为空时禁用注入 |
| `COVER_PROFILE_CONFIG_PATH` | 空 | 固定订阅配置 YAML 路径；设置时必须同时设置 `PRIVATE_CONFIG_PATH` |
| `DEBUG` | `false` | 设为 `true` 输出调试日志及 Mihomo 日志 |

`PROXY_PORT` 和 `API_PORT` 必须避免与容器内其他进程占用的端口冲突。`INTERNAL_BASE_URL` 必须是 Subconverter 容器能够访问的地址，不能填写客户端所见但容器无法访问的公网或宿主机地址。

`/sub` 请求指定 HTTP(S) `config` 时，Prefetch Proxy 会读取 INI，将其中 `ruleset=策略组,http(s)://...` 的规则地址替换为短期内部地址，然后把改写后的 INI 地址交给 Subconverter。规则文件由 Prefetch Proxy 在 Subconverter 请求内部地址时获取。非 HTTP(S) 规则（如 `[]GEOIP`、`[]FINAL` 和本地路径）原样保留。配置来源无法读取时转换请求返回 502；规则来源无法读取时内部规则地址返回 502，Subconverter 可能仍生成缺少该规则的输出，因此应检查其日志。配置内部地址有效期为 10 分钟，规则内部地址有效期为 21 分钟；同一规则短链仅在前 10 分钟内复用，确保新配置引用的规则地址不会先于配置过期。

若自行准备的 `new.ini` 以 `config/new.ini` 挂载到 Subconverter，需让 Prefetch Proxy 也能读取同一份文件。例如将宿主机上的 `new.ini` 再挂载为 `/shared-config/config/new.ini:ro`，并设置 `CONFIG_DIR=/shared-config`；请求中的 `config` 值仍为 `config/new.ini`。不设置 `CONFIG_DIR` 时，相对路径保持原样，由 Subconverter 自行读取，规则地址不会改写。修改共享 INI 时，请同时更新两个容器的挂载文件。

## Mihomo Observer

`mihomo-observer/` 从指定的 FlClash/Mihomo Controller 采集连接快照，保存 SQLite 历史，并通过 Basic Auth 保护的浏览器界面展示 Dashboard、目标、路径、问题与趋势。使用方式、配置示例及当前验收边界见 [Mihomo Observer README](mihomo-observer/README.md)。

本地构建：

```bash
docker build -t mihomo-observer:local ./mihomo-observer
```

`mihomo-observer/` 中的 Dockerfile、Go 源码与依赖、嵌入式网页资源或对应工作流发生变更并推送到 `main` 时，GitHub Actions 构建 `linux/amd64`、`linux/arm64` 镜像，发布 `mrxianyu/mihomo-observer:latest` 和 `sha-<提交 SHA>`。相关 PR 仅构建验证，也可手动触发。

## GitHub Actions 触发范围

三个镜像工作流各自只监听会进入镜像的所属组件文件及本工作流文件。修改 `subconverter/` 中的 INI 只会触发 Subconverter 构建；只修改 `rule-list/`、文档或其他组件不会触发无关镜像。PR 只验证构建，`main` 推送和手动触发才发布镜像；手动触发是显式重建入口。

## 本地开发

检查 Go 代码：

```bash
cd prefetch-proxy
go test ./...
go vet ./...
```

构建 Prefetch Proxy：

```bash
cd prefetch-proxy
go build ./...
```

检查跨组件转换配置契约：

```bash
python3 -m unittest discover -s tests
```

## 说明

- 本项目的模板和规则带有较强的个人使用偏好，部署前请检查策略组、DNS、Fake-IP 和规则内容是否适合自己的网络环境。
- 订阅预取依赖目标订阅能够先返回可用的 Clash YAML 前置节点。
- Prefetch Proxy 的缓存位于内存中，不适合用作持久订阅存储。
- 项目目前未提供稳定 API 兼容性承诺，升级前建议先在测试环境验证生成结果。
