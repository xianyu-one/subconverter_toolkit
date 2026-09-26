# Phase 3：Mihomo Controller 数据模型分析

状态：已核对 Mihomo 官方 `Alpha` 源码；用户主要使用 FlClash，并倾向于保持 Mihomo 主线新版本。实际运行的核心版本与脱敏响应尚待验证。本文件只定义首版可依赖的数据语义，不把未提供的字段补造成事实。

## 接口事实

| 接口 | 官方行为 | Observer 用法 |
| --- | --- | --- |
| `GET /connections` | 普通 HTTP 返回一次活动连接快照；WebSocket 升级后立即发送一次，随后按 `interval` 毫秒发送，默认 1000 ms | 首选 WebSocket；断线重连，必要时用 HTTP 快照作为同语义降级。每帧是完整活动集合，不是增量事件。[¹](https://github.com/MetaCubeX/mihomo/blob/Alpha/hub/route/connections.go) |
| `GET /traffic` | 约每秒发送全局 `up`、`down`、`upTotal`、`downTotal`；支持 WebSocket 或流式 HTTP | 记录 Mihomo 统计口径下的全局字节增量。首样本只作为基线；断线或计数器回退时标记缺口。[²](https://github.com/MetaCubeX/mihomo/blob/Alpha/hub/route/server.go) |
| `GET /proxies` | 返回当前代理目录 | 低频读取，用于解释路径名称和可用的组；不能据此还原过去每条连接的选择。[³](https://github.com/MetaCubeX/mihomo/blob/Alpha/hub/route/proxies.go) |
| `GET /proxies/{name}/delay` | 对给定 URL 执行主动测试 | 不进入被动采集 MVP；它不是连接历史里的被动延迟。[³](https://github.com/MetaCubeX/mihomo/blob/Alpha/hub/route/proxies.go) |
| `GET /version` | 返回 `meta` 与 `version` | 启动或重连时低频读取，记录实际核心版本，帮助解释 API 差异；不要求用户手工维护固定版本号。[²](https://github.com/MetaCubeX/mihomo/blob/Alpha/hub/route/server.go) |
| `GET /logs` | 实时日志流 | 首版不持久化，也不作为连接历史的主数据源。[²](https://github.com/MetaCubeX/mihomo/blob/Alpha/hub/route/server.go) |

Controller 有读写两类路由。Observer 只调用上述所需的读接口，不转发 Controller API 给浏览器。配置了 Mihomo `secret` 时，Go 客户端在 HTTP 与 WebSocket 握手中使用 `Authorization: Bearer ...`；源码也允许 WebSocket 查询参数 `token`，但 Observer 不使用这种会进入 URL 的方式。Observer Web UI 的单管理员认证是独立机制。[⁴](https://github.com/MetaCubeX/mihomo/blob/Alpha/hub/route/server.go)

## 连接快照与字段映射

顶层快照包含 `connections`、`uploadTotal`、`downloadTotal` 和 `memory`。连接来自当前 tracker 集合，元素顺序不稳定；没有活动连接时，Go 的 nil 切片可能编码为 `"connections": null`，须视为空集合。顶层流量累计值可用于与 `/traffic` 交叉检查，但首版目标级统计只从各 tracker 的累计值计算。快照**没有采样时间戳**，Observer 以接收并校验成功的本地时间记录 `observed_at`。[⁵](https://github.com/MetaCubeX/mihomo/blob/Alpha/tunnel/statistic/manager.go)

| Observer 概念 | Controller JSON 字段 | 处理与边界 |
| --- | --- | --- |
| 连接 ID | `id` | 官方 tracker 生成 UUID；仍与 API `start` 和观测世代组合识别持续连接，不能仅假设 ID 永不复用。 |
| API 开始时间 | `start` | 有值时解析带时区的时间，转 UTC 毫秒；缺失时保持未知并降低身份连续性判断能力。首次/最后观测时间由 Observer 另记。 |
| 精确结束时间、关闭原因、成功或失败 | 无 | 保持未知。只有连续有效快照中未再出现时，才可记录“发现其消失”的时间。 |
| 累计上传/下载 | `upload`、`download` | 非负整数；相邻已持久化样本的差额是已观测增量。可能漏掉消失前的最终字节。 |
| 规则与路径 | `rule`、`rulePayload`、`chains`、`providerChains` | 原样记录规则与有序链；空值表示未知。链顺序和组/节点语义须由实际客户端样本验证，不凭名称猜测 `final_proxy`。[⁶](https://github.com/MetaCubeX/mihomo/blob/Alpha/tunnel/statistic/tracker.go) |
| 源地址 | `metadata.sourceIP`、`sourcePort` | 字段可能为空；默认不持久化源 IP。端口在官方 JSON 中是**字符串**，兼容解析数字形式。 |
| 目标地址 | `metadata.destinationIP`、`destinationPort` | IP 可为空；空时不推断 IPv4/IPv6。端口同样以字符串传输。 |
| 域名 | `metadata.host`、`sniffHost` | 分开保存并标注来源；展示归组优先 `host`，缺失时用 `sniffHost`，再缺失才用目标 IP。两者不同须在详情中显示。 |
| DNS 域名 | 无独立 `dnsHost` | 不设置伪造的 `dns_host`；`dnsMode` 是模式信息，不是域名映射。 |
| IP 版本 | 由有效 `destinationIP` 解析 | `4`、`6` 或未知；不从域名、代理出口或 `remoteDestination` 推断。 |
| 网络与入站类型 | `metadata.network`、`type` | 网络值按字符串保留并识别 `tcp`、`udp` 等已知值；未知值仍可存储。`type` 是入站类型，不能当作 HTTP、TLS、QUIC、WebSocket 或 SSH 的可靠应用协议分类。 |
| ASN | `metadata.destinationIPASN` | 字符串可能为空；先作为不透明的 API 值保存，能确认其规范格式后再考虑提取 ASN 编号。 |
| 进程 | `metadata.process`、`processPath` | 由平台和客户端能力决定；默认不持久化，启用时也允许空值。 |
| 其他连接元数据 | `metadata.dnsMode` 等 | 只保存白名单中确有分析用途的字段，不持久化整段 metadata。[⁷](https://github.com/MetaCubeX/mihomo/blob/Alpha/constant/metadata.go) |

`metadata.remoteDestination` 与 `destinationIP` 是不同字段，不能用作缺失目标 IP 的替身；它由连接的 `RemoteDestination()` 提供，可能描述出站路径上的远端。用它推断 IPv6 目标或目标 ASN 会污染分析。[⁶](https://github.com/MetaCubeX/mihomo/blob/Alpha/tunnel/statistic/tracker.go) [⁷](https://github.com/MetaCubeX/mihomo/blob/Alpha/constant/metadata.go)

Mihomo 内部的 `RuleHost()` 在 `sniffHost` 非空时优先返回它，Observer 为了保持此前确认的归组口径仍优先 `host`。因此“展示归组域名”和“规则可能使用的域名”不能混为一个值；详情页应同时显示两者和实际 `rule`。[⁷](https://github.com/MetaCubeX/mihomo/blob/Alpha/constant/metadata.go)

## 解码边界

连接适配层把 Controller 原始 JSON 解码为可空的观测模型，再交给 Collector。它只承担类型转换和校验，不负责推断异常。建议的逻辑字段为：`external_id`、`api_started_at`、`observed_at`、`host`、`sniff_host`、`destination_ip`、`destination_port`、`ip_version`、`network`、`inbound_type`、`rule`、`rule_payload`、`chains`、`provider_chains`、`upload`、`download`，以及可选的源地址、进程和 ASN。

解码规则：

1. 接受 JSON 未知字段，不把它们自动写入数据库；字段缺失、`null`、空字符串分别按实际语义处理。`metadata` 为空时仍可保留连接 ID 与字节计数，但目标未知。
2. 端口接受数字字符串以及兼容的数字形式，范围限制为 0–65535。IP 通过标准 IP 解析器验证；无效或空值保持未知，IPv4 映射地址规范化后再决定版本。
3. `start` 缺失可保留为未知，但非空且格式错误、累计字节数错误或链元素类型错误时，拒绝该条连接并计入解码错误；顶层 `connections` 接受数组或 `null`（空集合），缺失或其他类型则拒绝**整帧**。含无效连接的帧也不能用于判定旧连接消失。
4. 同一帧若出现重复 ID 或计数器回退，隔离异常身份，不写负字节增量。只有完整有效且连续的快照才能用于判断连接消失；断线、解码失败和队列丢样产生采集缺口。
5. 给 Web UI 的领域模型只使用白名单字段。URL、密钥、原始响应及可能含敏感内容的日志不进入存储或错误报告。

## 能支持和不能支持的分析

| 分析 | API 能提供的证据 | 首版结论上限 |
| --- | --- | --- |
| 短时重复连接 | 多个可见 ID 在相近时间出现，目标与路径相同 | “重复连接迹象”；不能证明浏览器重试或请求失败 |
| 小流量长连接 | 累计字节增量小、连续多帧可见 | 需其他迹象佐证；不能识别所有 WebSocket、SSH、Streaming 或 Keepalive |
| IPv4/IPv6 差异 | 有效目标 IP、目标域名、相同规则路径、两侧样本 | “疑似 IPv6 路径问题”；不能证明回退事件、超时率或真实失败率 |
| Proxy/ASN 差异 | `chains` 与可选 ASN，加上已观测字节和连接模式 | 描述历史分布与异常证据；不能推断被动连接延迟或最佳节点 |

## 实际客户端待核验清单

官方 `Alpha` 分支不是用户设备的版本保证。FlClash 当前源码会在生成运行配置时重写 `external-controller`，所以本仓库订阅模板中的 Controller 地址不能当作实际监听地址。其当前枚举只给出关闭或 `127.0.0.1:9090`，与架构说明中的默认关闭、启用后只监听回环一致。应以运行中的 FlClash 设置和 `/version` 响应为准。[FlClash 配置生成代码](https://github.com/chen08209/FlClash/blob/main/lib/common/task.dart)、[监听状态枚举](https://github.com/chen08209/FlClash/blob/main/lib/enum/enum.dart)、[FlClash 架构说明](https://github.com/chen08209/FlClash/blob/main/.agents/architecture.md)

用户已确认首版 Windows 使用原生 Observer 连接本机 FlClash；Docker 用于容器确实可以访问 Controller 的环境，例如适当配置的 Linux host network。不为 Windows Docker 增加环回端口转发组件，也不要求 FlClash 暴露 Controller 到局域网。

Phase 4 开始编码前，用实际 Linux/Windows 客户端分别确认：

- 客户端名称、Mihomo 版本、External Controller 地址与鉴权方式，以及 Docker/原生程序是否可访问。
- `/connections` 首帧和后续帧的顶层字段；空连接、IP-only、域名、IPv4、IPv6、DIRECT 与代理链各取至少一种脱敏样本。
- `sourcePort`/`destinationPort` 的实际 JSON 类型，`destinationIP`、`sniffHost`、`destinationIPASN`、`providerChains` 的出现条件，`chains` 的顺序与组名含义。
- Mihomo 重启、Observer 重连、睡眠恢复时，连接 ID、`start`、累计字节数及 `/traffic` 总计是否重置。
- `metadata.network` 是否出现 `tcp`/`udp` 之外的值；即使出现，也只作为元数据保存，不直接等同应用协议。

脱敏样本可替换域名、IP、进程名和节点名，但应保留字段名、JSON 类型、空值、数组顺序与相对时间。没有样本时，以上项目保持“待验证”，不假定所有桌面客户端行为相同。
