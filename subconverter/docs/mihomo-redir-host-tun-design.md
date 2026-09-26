# Mihomo Redir-Host + TUN 方案

核对日期：2026-09-25。依据 [Mihomo DNS](https://wiki.metacubex.one/en/config/dns/)、[TUN](https://wiki.metacubex.one/en/config/inbound/tun/)、[Sniffer](https://wiki.metacubex.one/en/config/sniff/)、[dialer-proxy](https://wiki.metacubex.one/en/config/proxies/dialer-proxy/) 官方文档；平台行为参照 [Nikki 默认配置](https://github.com/nikkinikki-org/OpenWrt-nikki/blob/main/nikki/files/nikki.conf)、[OpenClash 配置覆写](https://github.com/vernesong/OpenClash/blob/master/luci-app-openclash/root/etc/openclash/overwrite/default) 与 [FlClash 项目](https://github.com/chen08209/FlClash)。

## 已确定的目标与边界

- `all_base.tpl` 新增 URL 参数选择 Redir-Host，保留现有 Fake-IP 默认行为。新方案启用 TUN 与 Sniffer，并继续支持 `dialer-proxy` 注入的前置节点与单个落地节点。
- 保留公开的 `all-online.ini` 与自行维护的 `new.ini` 原有分流组和 `DIRECT` 规则；仅允许在规则前部增加少量加密 DNS 与 WebRTC 的优先代理规则，指向 `🔰 节点选择`。
- 客户端可继续使用 Android 私人 DNS 或浏览器安全 DNS。进入 TUN 的普通 DNS 由内部 DNS 接管；客户端自身的 DoH/DoT/DoQ 连接按流量处理，针对明确列出的解析器强制走主代理组。
- 节点域名和 DNS 服务引导解析使用固定 IP 的加密 DNS，避免通过 `system` 或明文 DNS 引导。引导连接可以直达解析器；其他解析请求按预期代理出口处理。
- 可靠性、隐私优先。节点、DNS 或 TUN 失败时不自动回退直连。IPv6 无法可靠经代理转发时阻断 IPv6。
- WebRTC 功能保留；已识别的 STUN/TURN 流量优先走主代理组。Sniffer 只辅助恢复规则匹配所需域名，不改变原始目标 IP。
- OpenWrt 的保护范围是默认网关和 DNS 指向旁路由的设备。未经过旁路由的设备不应被旁路由的 DNS 接管规则影响。

## 核心配置

1. `target=clash` 且 `clash.redir-host=1` 时启用新模式；其他请求保留 Fake-IP 默认行为与 `clash.tun-set` 旧参数。Redir-Host 分支使用 `dns.enhanced-mode: redir-host`，不输出 Fake-IP 专属键和 Include 名单。
2. Redir-Host 分支设置 `ipv6: true`、`auto-route: true`、`strict-route: true`，同时劫持 UDP/TCP 53。默认全局路由由 Mihomo 创建，不指定固定设备名和额外的路由网段，以便客户端按平台管理 TUN。
3. `proxy-server-nameserver` 使用固定 IP 的加密 DoH 并显式直连引导；普通 `nameserver` 使用 `🔰 节点选择` 代理组。直连站点使用独立的加密 `direct-nameserver`。关闭 `prefer-h3`，避免与 `respect-rules` 的官方不推荐组合。
4. Sniffer 启用 HTTP/TLS/QUIC 常用端口，`force-dns-mapping` 与 `parse-pure-ip` 用于恢复可见域名，`override-destination: false` 保留原始目标 IP。
5. README 记录请求示例和客户端覆写检查方法。两份 INI 的优先规则包含常见加密 DNS 解析器，以及 853、784、8853、3478、5349、19302 端口；需要按实际解析器清单补齐。

## 平台适配

| 平台 | 必须检查的本机设置 |
| --- | --- |
| Nikki / OpenWrt | 确认运行配置的 DNS 模式、TUN、IPv4/IPv6 接管、LAN 访问控制和 DNS 接管与订阅一致；仅对经过旁路由的设备施加策略。Nikki 有独立的 UCI DNS、TUN、Sniffer 和路由设置。 |
| OpenClash / OpenWrt | 选择 Redir-Host TUN 运行模式；检查 DNS、IPv6、TUN 等覆写项，避免其重写订阅值。网关设备的转发、防火墙与 IPv6 路由需要单独核验。 |
| FlClash / Android | 启用 VPN/TUN，确认 VPN 覆盖应用范围；私人 DNS 不会被 `dns-hijack` 直接劫持，须确认其加密连接进入 VPN。需要系统的常驻 VPN 与阻止无 VPN 连接来处理客户端退出后的流量。 |
| FlClash / Windows、Linux | 确认 TUN 实际启用、运行配置未被客户端 DNS/路由覆写；Windows 的 `strict-route` 有 DNS 防泄漏作用，Linux 还需检查策略路由与防火墙。客户端退出后的阻断需操作系统防火墙实现。 |

## 不能由通用模板单独保证的事

- Mihomo 只处理实际进入其 TUN 或路由路径的包。旁路由无法保护没有经过它的设备；客户端退出、VPN 断开或路由器防火墙失效时需要系统级阻断。
- 任意 DoH 可以使用 HTTPS/443，可能与普通网页共用 IP，或隐藏域名。固定解析器列表之外的 DoH 无法靠有限规则全部识别。此处先用常见解析器作为示例，用户稍后提供实际清单再补齐。
- Sniffer 只支持 HTTP、TLS、QUIC 的可见信息；关闭它不会阻止 IP 连接、普通 DNS 或 `dialer-proxy` 建链，但失去部分域名规则匹配能力。端到端加密或纯 IP 流量仍可能只匹配 IP 规则。
- WebRTC 的 STUN/TURN 服务可以使用不同端口。TUN 和优先规则控制网络出口，但浏览器暴露的本地候选地址还需浏览器自身策略控制。

## 验收

已在本地分别渲染默认 Fake-IP、Fake-IP + TUN、Redir-Host + TUN、ClashR 四种模板路径并通过 YAML 解析；Redir-Host 配置与新优先规则通过 Mihomo v1.19.31 的 `-t` 检查。还需要在实际 OpenWrt、Android、Windows、Linux 设备上检查 A/AAAA、UDP/TCP 53、已列出的 DoH/DoT/DoQ、WebRTC、节点切换和断线后的出口；旁路由测试还需验证未使用旁路由的设备不受影响。
