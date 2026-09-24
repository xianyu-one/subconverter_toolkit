# Subconverter Toolkit

一套围绕 [Subconverter](https://github.com/tindy2013/subconverter) 和 [Mihomo](https://github.com/MetaCubeX/mihomo) 构建的订阅转换工具集，包含自定义转换镜像、Clash/Mihomo 配置模板、规则集、Include 检查工具，以及用于处理特殊订阅的预取代理。

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

## 项目结构

| 路径 | 用途 |
| --- | --- |
| `Dockerfile` | 构建自定义 Subconverter 镜像 |
| `subconverter_server_conf/` | 服务端配置、基础模板、Emoji 规则和 Fake-IP Include 文件 |
| `all-online.ini` | 仅依赖在线规则的转换配置 |
| `new.ini` | 私有部署使用的转换配置，包含本地规则和链式代理策略组 |
| `rule-list/` | 项目维护的规则列表 |
| `check_include.py` | Include 文件格式与重复项检查工具 |
| `prefetch-proxy/` | Subconverter 前置代理，用于二次订阅、域名提取和私有节点注入 |

## 自定义 Subconverter 镜像

根目录的 `Dockerfile` 基于官方 `tindy2013/subconverter:latest`，将以下内容写入镜像：

- `subconverter_server_conf/pref.toml`：Subconverter 服务端配置
- `subconverter_server_conf/all_base.tpl`：各客户端的基础配置模板
- `subconverter_server_conf/emoji.toml`：节点名称 Emoji 规则
- `subconverter_server_conf/include/`：Clash/Mihomo 的 Fake-IP Filter 内容

仓库的 GitHub Actions 会在推送到 `main` 分支及每月定时任务中构建 `linux/amd64`、`linux/arm64` 镜像，并发布为：

```text
mrxianyu/subconverter:latest
mrxianyu/subconverter:<上游 Subconverter 版本>
```

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
https://raw.githubusercontent.com/xianyu-one/subconverter-toolkit/main/all-online.ini
```

示例：

```text
https://your-subconverter.example/sub?target=clash&url=<订阅地址>&config=https%3A%2F%2Fraw.githubusercontent.com%2Fxianyu-one%2Fsubconverter-toolkit%2Fmain%2Fall-online.ini
```

### `new.ini`

私有部署版本。在 `all-online.ini` 的基础上增加了：

- 内网规则 `http://caddy-local/rule-list/xianyudomain.list`
- `🔒 私有出口选择` 策略组
- `🚀 前置节点池` 负载均衡策略组
- 与 Prefetch Proxy 私有节点注入功能配套的链式代理策略

这个文件依赖部署环境中的 `caddy-local` 主机名，并不适合直接在公共 Subconverter 实例上使用。使用前请修改其中的内网规则地址，确保 Subconverter 容器能够访问它。

配合私有节点注入时，请通过 Prefetch Proxy 请求转换接口，并添加正确的 `chaintoken`：

```text
https://your-prefetch-proxy.example/sub?target=clash&url=<订阅地址>&config=<new.ini 地址>&chaintoken=<令牌>
```

Prefetch Proxy 会在转发前删除 `chaintoken`，不会将它传给 Subconverter。

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

`check_include.py` 用来检查 `subconverter_server_conf/include/` 一类 YAML 列表文件。它仅依赖 Python 3.9+ 标准库，会扫描所选目录第一层的所有 `.txt` 文件并检查：

- LF、CRLF 和孤立 CR 换行
- 文件末尾是否存在换行符
- UTF-8 BOM、空白行和 Tab 缩进
- 不符合 `- domain` 形式的列表项
- 单个文件内及多个文件之间的重复域名

运行：

```bash
python3 check_include.py
```

按照提示输入目录；在支持 GNU Readline 的环境中可以使用 Tab 补全：

```text
subconverter_server_conf/include
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
  -> Subconverter
  -> 转换结果
```

### 构建镜像

```bash
docker build -t prefetch-proxy:latest ./prefetch-proxy
```

当前 `prefetch-proxy/Dockerfile` 内置的是 Mihomo `v1.18.2` 的 `linux-amd64` 二进制，因此该镜像当前只适合 AMD64 环境。若要部署到 ARM64，需要调整 Mihomo 下载目标。

仓库的 `Prefetch Proxy Docker Build` 工作流会在相关 PR 上验证镜像构建；相关变更进入 `main`、每月定时或手动触发时，会使用现有的 `DOCKERHUB_USERNAME` 与 `DOCKERHUB_TOKEN` 仓库密钥发布 `mrxianyu/prefetch-proxy:latest` 和对应的 `sha-<提交 SHA>` 标签。该工作流仅构建 `linux/amd64`。

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

请将含真实令牌和节点密码的配置文件保留在仓库外，并以只读卷挂载。配置格式和校验规则详见 [设计文档](docs/prefetch-proxy-private-config-design.md)。

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
          config: https://example.com/custom.ini
          filename: abc
      "2":
        upstreams: [first, second, third]
        params:
          target: clash
          exclude: "^test"
```

设置 `COVER_PROFILE_CONFIG_PATH=/run/secrets/cover-profiles.yaml`，并将两个文件以只读卷挂载。客户端使用：

```text
https://your-prefetch-proxy.example/sub?chaintoken=<令牌>&coverprofile=1
```

请求中的 `url` 会整体替换配置中的上游列表；`target`、`config`、`exclude`、`include`、`filename` 等参数逐项覆盖文件默认值。显式空 `exclude=` 可清除文件值，空 `url=` 会报错。不带 `coverprofile` 的旧 `/sub` 请求继续使用客户端提供的参数。

多个上游中只要有一个提供可解析节点即可继续转换；全部失败时返回错误，私有节点不计入成功上游。代理会跳过预取失败的上游；普通上游的部分失败依赖后端 Subconverter 启用 `skip_failed_links = true`，本仓库提供的 `pref.toml` 已启用。完整格式及错误行为见[固定订阅配置设计](docs/prefetch-proxy-cover-profiles-design.md)。

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
| `RULE_LIST_PATH` | 空 | 节点域名 Rule List 的写入路径；为空时禁用 |
| `FAKE_IP_FILTER_PATH` | 空 | 节点域名 Fake-IP Filter 的写入路径；为空时禁用 |
| `PRIVATE_CONFIG_PATH` | 空 | 私有节点、组和密钥 YAML 路径；为空时禁用注入 |
| `COVER_PROFILE_CONFIG_PATH` | 空 | 固定订阅配置 YAML 路径；设置时必须同时设置 `PRIVATE_CONFIG_PATH` |
| `DEBUG` | `false` | 设为 `true` 输出调试日志及 Mihomo 日志 |

`PROXY_PORT` 和 `API_PORT` 必须避免与容器内其他进程占用的端口冲突。`INTERNAL_BASE_URL` 必须是 Subconverter 容器能够访问的地址，不能填写客户端所见但容器无法访问的公网或宿主机地址。

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

## 说明

- 本项目的模板和规则带有较强的个人使用偏好，部署前请检查策略组、DNS、Fake-IP 和规则内容是否适合自己的网络环境。
- 订阅预取依赖目标订阅能够先返回可用的 Clash YAML 前置节点。
- Prefetch Proxy 的缓存位于内存中，不适合用作持久订阅存储。
- 项目目前未提供稳定 API 兼容性承诺，升级前建议先在测试环境验证生成结果。
