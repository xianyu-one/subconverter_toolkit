# Prefetch Proxy 固定订阅配置设计

## 目标

客户端持有稳定的 `/sub` 地址。上游订阅地址变化时，维护者只修改 Prefetch Proxy 的配置文件并重启服务；无需逐台修改客户端。一个密钥可以选择多份订阅配置，每份配置可合并同一密钥下的若干命名上游，并提供 Subconverter 查询参数的默认值。

## 领域关系

- 现有 `chaintoken` 是唯一的密钥凭据。密钥管理名称来自现有 `PRIVATE_CONFIG_PATH` 中的 `keys`；订阅配置文件按该名称引用密钥，不保存第二份 token。
- 一个密钥有自己的命名上游集合与订阅配置集合。不同密钥可以使用相同的配置名或上游名，它们互不引用。
- 一份订阅配置按顺序引用至少一个已命名上游。相同上游可被同一密钥下的多份配置引用。
- 密钥可以仅访问订阅配置，不选择私有节点。因此现有私有配置须允许没有 `proxies`，以及未选择节点的 key；已有私有节点选择语义保持不变。

## 配置文件草案

订阅配置单独存放在 YAML 文件中，以新的环境变量 `COVER_PROFILE_CONFIG_PATH` 指定。下面的地址和令牌仅为示例：

```yaml
# PRIVATE_CONFIG_PATH 指向的现有密钥文件
keys:
  - name: a
    token: replace-with-a-long-random-token
```

```yaml
# COVER_PROFILE_CONFIG_PATH 指向的订阅配置文件
keys:
  - name: a
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
          user_agent: 'CustomClient/1.0'  # 代理拉取上游和请求 Subconverter 时使用
      "2":
        upstreams: [first, second, third]
        params:
          target: clash
          config: https://example.com/custom.ini
          include: '(香港|日本)'
          exclude: '(到期|剩余流量)'
```

`params` 是请求参数名到字符串值的映射，包括 `target`、`config`、`exclude`、`include`、`filename`，以及 Prefetch Proxy 使用的 `user_agent`、`nocache`。`url` 由 `upstreams` 生成；`chaintoken` 与 `coverprofile` 不能写入 `params`。

在固定订阅配置文件中设置 UA 时，在对应配置的 `params` 下写示例中的 `user_agent`。YAML 中填写原始字符串，无须 URL 编码；请求 URL 中的 `&user_agent=Custom%2F2` 可覆盖文件值。该 UA 用于 Prefetch Proxy 拉取订阅、远程 `config`、规则集及请求 Subconverter。规则集由 Subconverter 稍后通过内部短链触发拉取，短链会保留创建时选定的 UA，并按规则 URL 与 UA 分开复用。修改配置文件后重启服务生效。

`include`、`exclude` 的值是匹配节点名称的正则表达式字符串：前者只保留匹配节点，后者排除匹配节点。YAML 中填写原始表达式，不要预先 URL 编码，也不能写成列表；建议使用单引号，避免正则中的反斜杠被 YAML 双引号当作转义字符。多个关键词任选其一时写 `include: '(香港|日本)'`；必须同时出现且不限定顺序时写 `include: '(?=.*香港)(?=.*专线)'`。`exclude: '(到期|剩余流量)'` 会排除名称含任一关键词的节点。Prefetch Proxy 在转发查询参数时会自动编码这些值；手写 `/sub` URL 时才需要编码。正则的 `|` 与上游 `url` 条目间的 `|` 分隔符用途不同。

命名上游的值遵守 Subconverter 单个 `url` 条目的格式，包括 HTTP(S) 订阅地址、节点分享链接等。多个条目按配置顺序用 `|` 组合。所有 HTTP(S) 订阅均由 Prefetch Proxy 拉取并缓存，再以内部 URL 交给 Subconverter；命中 `TARGET_DOMAINS` 的地址仍走两阶段预取流程。非 HTTP(S) 条目原样交给 Subconverter。

启动时严格校验 YAML、密钥名称、上游引用和每份配置的非空上游列表。文件缺失或校验失败时拒绝启动。修改配置文件后重启服务生效；不要求运行中热加载。

## 请求和优先级

客户端选择配置：

```text
/sub?chaintoken=<token>&coverprofile=1
```

`coverprofile` 只在 `/sub` 上生效，转发给 Subconverter 前移除。提供它时必须有有效的 `chaintoken`：缺少或错误的 token 返回 403；该密钥下不存在指定配置返回 404，响应不列出配置名或上游地址。

不提供 `coverprofile` 的请求沿用旧接口的参数来源，包括客户端传入的 `url`、`config` 和其他 Subconverter 参数。旧接口不自动套用文件中的任何订阅配置，`chaintoken` 仍可用于选择私有节点；多上游失败规则按下文统一调整。

提供 `coverprofile` 时，文件中的上游与 `params` 构成默认请求。客户端查询参数按参数名覆盖默认值：

- 显式 `url=` 整体替换该配置的上游组合，不与之合并；若覆盖后的全部上游失败，不回退到文件中的上游。
- `target`、`config`、`exclude`、`include`、`filename` 及其他 Subconverter 参数各自覆盖同名默认值。
- 显式传入的空值清除对应的可选默认参数；显式空 `url=` 返回错误。
- 转发时移除 `chaintoken`、`coverprofile`、`user_agent` 和 `nocache`，保留其他 Subconverter 参数的原有含义。若文件和请求都没有 `target`，由 Subconverter 按其规则处理。`user_agent` 会设置代理拉取订阅、远程 `config`、规则集及发往 Subconverter 请求的 User-Agent 头，URL 参数优先于文件默认值；未设置时使用 `FlClash/v0.8.98 clash-verge Platform/windows`。
- `nocache=1` 强制刷新全部 HTTP(S) 上游及普通订阅域名提取标记。刷新其中任何上游失败时本次请求返回错误；之前仍有效的缓存不被失败结果覆盖。
- 若密钥选中私有节点，继续按现有方式注入；没有私有节点的密钥不注入内部私有节点链接。

## 多上游失败规则

这条规则同时适用于带 `coverprofile` 的请求和旧接口的多上游 `url=A|B` 请求。

- 上游只有提供至少一个可解析节点才算可用；HTTP 成功但内容没有可解析节点仍算失败。
- 多个上游中至少一个可用时，继续转换可用上游的节点；失败上游不阻断结果。成功上游在组合中的相对顺序保持不变。
- 所有上游都失败时返回错误。私有节点是附加内容，不计入可用上游；即使私有节点可用也要报错。
- 不使用过期缓存兜底。仍在有效期内的成功预取缓存可以作为本次可用内容。
- 只有上游失败可以被跳过；`config`、`target`、转换模板等最终转换错误仍返回错误。

Prefetch Proxy 会在转发前检查每个 HTTP(S) 上游是否产生可解析节点，跳过失败项；不依赖后端的 `skip_failed_links` 处理这些地址。`nocache=1` 的强制刷新采用更严格的失败规则：任一 HTTP(S) 上游失败即返回错误。订阅内容缓存与普通订阅域名提取标记共用 `PREFETCH_CACHE_TTL`，默认 `3h`，并按上游地址及最终 User-Agent 区分。

## 兼容性检查场景

| 请求或状态 | 预期行为 |
| --- | --- |
| 旧 `/sub?url=A\|B&target=clash` | 不读取固定订阅配置；一个上游可用即可继续 |
| `/sub?chaintoken=T&coverprofile=1` | 使用 T 对应密钥的配置 1 |
| 上述请求再传 `filename=client` | 使用 `client`，覆盖文件中的 `abc` |
| 上述请求再传 `url=C` | 仅使用 C；C 失败则报错 |
| 配置中的三个上游有两个失败 | 使用剩余可用上游继续转换 |
| 全部上游失败，仅私有节点可用 | 报错 |
| 未知配置名或属于另一密钥的配置名 | 404 |
| 修改上游地址并重启服务 | 同一客户端地址开始使用新上游 |
