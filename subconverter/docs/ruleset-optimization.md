# all-online 规则集优化

本轮沿用已有策略组和默认选择，保留前一轮确认的 Steam 分流。规则匹配遵循先匹配先处理，因此修复的是实际覆盖关系，而非仅按名称排序。

## 已修复的问题

| 问题 | 调整 | 代表性匹配 |
| --- | --- | --- |
| Google 的 `DOMAIN-KEYWORD,google` 和 YouTube 后缀抢先匹配 | YouTube、Gemini 放到 Google 之前 | `youtubei.googleapis.com`、`yt3.ggpht.com` → YouTube；`gemini.google.com` → AI |
| Microsoft 的 `azureedge.net` 抢先匹配 OpenAI 专用端点 | OpenAI 专属域名在 Microsoft 之前 | `openaiapi-site.azureedge.net` → AI |
| OpenAI 已有 ACL4SSR 列表 | 直接引用上游 `OpenAi.list` | 保持 AI 优先级，服务范围跟随上游 |
| Amazon 已有 ACL4SSR 列表 | 直接引用上游 `Amazon.list` | Amazon 站点、影音与 AWS 覆盖跟随上游 |
| Netflix IP 列表提前匹配，可能覆盖其他域名策略 | 移到业务和国内域名之后、国内 IP 兜底之前 | 能识别的域名先按域名策略；未匹配的 Netflix IP 保留 Netflix 策略 |
| 自维护列表引用旧仓库 `subconverter-toolkit`，6 个来源返回 404 | 两份 INI 均修正为实际仓库 `subconverter_configs` | 保持 lite 分流顺序和策略，仅修复来源 |
| 同组重复来源 | 删除 ClaudeAI、YouTubeMusic、CN、ChinaDNS、ChinaNet | 已被保留来源覆盖 |
| Plex 的精确域名与相同后缀重复 | 只保留三个后缀规则 | 根域名和子域名均直连 |
| 上游 `UnBan.list` 将 Chrome 更新下载域名 `dl.google.com` 提前直连 | 按用户需求从两份配置移除 UnBan 与广告拦截来源 | `all` 的 Google 通用规则、`lite` 的最终规则负责代理 |
| 上游 ChinaDomain 未覆盖部分国内抖音资源域名 | 两份配置均引用自维护的抖音直连补充列表 | `douyinvod.click`、`idouyinvod.com`、`douyinvideo.net` → DIRECT |

公开来源覆盖核查时间：2026-10-04。Claude 与 ClaudeAI 当前内容相同；YouTubeMusic 的 `music.youtube.com` 被 YouTube 后缀覆盖；CN、ChinaDNS、ChinaNet 的全部条目已在 ChinaDomain 中。上游列表会变化，删除重复来源的判断不应视为永久不变。

GoogleFCM 含额外推送 IP，GoogleCN 含补充域名，GoogleCNProxyIP 含国内代理 IP，不能因为名称类似而删除。Bilibili 与 ChinaMedia 也不完全相同，保留补充覆盖。

## 来源与维护

ACL4SSR 已有的业务规则直接引用上游，不维护复制或裁剪列表。OpenAI 保留上游对 Auth0、Stripe、Sentry 等共享服务的覆盖，Amazon 保留上游的 `amazonaws.com` 和 AWS IP；接受这些范围是优先使用上游规则的结果。若需例外，使用少量经过核实的高优先级规则，而不是复制整份业务列表。

两份配置都不再引用 UnBan、BanAD 或 BanProgramAD，也不生成广告拦截策略组。自维护列表用于个人直连、国内抖音资源和 Android 连通性需求。`all` 当前包含 47 个 ruleset 来源，低于服务端的 64 个来源上限。

## 验证与边界

回归测试使用捕获的 Google、YouTube、Gemini、Microsoft、OpenAI、Amazon 上游样本及个人补充列表，检查业务优先级、上游引用与覆盖范围、IP 补充位置、来源上限和自维护路径。运行 `python3 -m unittest discover -s tests`。测试样本只用于验证，不作为运行时规则来源。

在线来源核查确认修正链接可获取。实际生成验证使用公开规则的本地副本与合成节点，不调用真实订阅；请求显式设置 `geoxset=0`，避开现有模板在缺少该参数时的渲染问题。第三方 DNS、仅含 IP 的连接和客户端覆写仍需查看运行中的最终配置与连接记录。

未根据未经测量的速度把 Apple、OneDrive 或其他下载业务统一改为直连。上游对共享 CDN 的分类可能宽泛，后续若出现具体误分流，优先添加核实的业务域名例外。
