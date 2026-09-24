# Prefetch Proxy 多密钥与私有节点设计

## 目标与边界

将私有节点、节点组和密钥放在同一份 YAML 中，使一个 Prefetch Proxy 实例能按不同密钥提供不同的节点集合。其余运行配置继续使用现有环境变量。现有单文件 Go 程序按职责拆成薄入口与可单独测试的内部包；普通转发、预取和规则文件更新保持现有行为。

本设计直接替换 `CHAIN_TOKEN`、`PRIVATE_NODES_PATH` 和固定的 `/internal/private`；不提供旧配置兼容期。`new.ini` 中私有节点前缀和前置节点池的约定保持不变。

## 配置文件

通过 `PRIVATE_CONFIG_PATH` 指定文件路径。未指定时，私有节点功能关闭，程序继续提供普通转发和预取；指定了路径但文件不可读取或内容无效时，启动失败。文件仅在启动时读取，更新后需要重启。

```yaml
proxies:
  - name: home
    groups: [personal, shared]
    type: ss
    server: example.com
    port: 443
    cipher: aes-128-gcm
    password: example-password
  - name: office
    groups: [work]
    type: ss
    server: office.example.com
    port: 443
    cipher: aes-128-gcm
    password: another-example-password

keys:
  - name: family
    token: replace-with-a-secret
    nodes: [home]
    groups: [shared]
  - name: colleague
    token: replace-with-another-secret
    groups: [work]
```

`proxies` 沿用 Clash 节点字段；`groups` 是 Prefetch Proxy 的节点标签，不传给 Subconverter。组由节点标签形成，不单独定义成员清单。每个节点名称和每个密钥管理名称在各自列表中唯一；密钥令牌也必须唯一。节点名称、密钥名称、令牌和引用项不得为空；`nodes` 与 `groups` 两种引用可只使用其中一种。每条密钥最终必须选到至少一个节点。引用不存在的节点或没有成员的组是配置错误。

节点名称引用与组引用取并集。按 `proxies` 的原始顺序输出，每个节点最多输出一次。输出节点名称保留 `🔒私有 - ` 前缀规则，并将 `dialer-proxy` 设为 `🚀 前置节点池`；输入中的 `groups` 不出现在输出里。

启动校验给出明确的配置位置与原因，且不得把令牌或节点密码写入日志。

## 请求与内部读取

客户端继续以 `chaintoken` 查询参数提交令牌。未提供时按普通请求处理；明确提供但不匹配时返回 HTTP 403，不进行私有节点注入。无论有无 `url` 参数，转发给 Subconverter 的请求都移除 `chaintoken`。没有 `url` 参数时没有可追加私有节点的订阅列表，因此仅执行令牌校验与擦除。

有效令牌对应的节点集合通过一个新生成的内部链接作为额外订阅加入 `url` 参数。链接使用密码学安全随机值，不含原始令牌；它仅能读取这次授权选中的节点。链接在创建后 10 分钟内可重复读取，过期或进程重启后失效。访问不存在或失效的链接返回 HTTP 404。固定的 `/internal/private` 不再提供私有节点。

内部链接是限时访问凭据。服务需要定期清理过期记录，且不得将链接或令牌记入常规日志。即使 Prefetch Proxy 的监听端口可由外部访问，仅知道固定路径也无法读取私有节点。

## Go 代码边界与验证

入口只负责加载配置、组装依赖和启动 HTTP 服务。内部包分别承担配置解析及校验、私有节点选择及授权链接、HTTP 请求处理、订阅预取和规则文件更新；用显式依赖代替当前全局配置与缓存。Dockerfile 的构建入口随目录调整。

验证至少覆盖：多密钥分别选择、名称与组的并集及顺序、重复命中去重、无效配置启动失败、无效令牌 403、所有请求擦除 `chaintoken`、内部链接只能读取对应节点、重复读取与过期、旧固定入口不可读取、未配置文件时普通代理仍可用。现有预取与规则文件更新也应通过回归测试验证。
