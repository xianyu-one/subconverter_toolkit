package main

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 环境配置结构，存放所有从环境变量读取的配置项
type Config struct {
	ListenAddr       string   // 服务监听地址，默认 :8080
	SubconverterURL  string   // 后端真实的 Subconverter 地址
	TargetDomains    []string // 需要被拦截并执行二次代理（预获取）的目标域名列表
	MihomoPath       string   // Mihomo 可执行文件的绝对路径
	ProxyPort        int      // 临时拉起的 Mihomo 提供的 Socks5 代理端口
	ApiPort          int      // 临时拉起的 Mihomo 提供的 External Controller API 控制端口
	InternalBaseURL  string   // 用于 Subconverter 访问本服务缓存的内部地址
	Debug            bool     // 是否开启调试日志输出
	RuleListPath     string   // 提取真实节点域名后，写入的规则集文件路径
	FakeIPFilterPath string   // 提取真实节点域名后，写入的 Fake-IP 模板文件路径
	ChainToken       string   // 用于链式代理注入鉴权的专属 token
	PrivateNodesPath string   // 私有节点文件路径
}

// Node 表示单个节点配置的通用字典结构
type Node map[string]interface{}

// ClashConfig 映射 Clash/Mihomo 的基础 YAML 配置文件格式
type ClashConfig struct {
	Proxies []Node `yaml:"proxies"`
}

// CacheItem 缓存数据项，包含数据体和过期时间
type CacheItem struct {
	Data      []byte
	ExpiresAt time.Time
}

// CacheManager 简单的并发安全内存缓存，用于缓存已经预获取过的订阅结果
type CacheManager struct {
	mu    sync.RWMutex
	items map[string]CacheItem
}

var (
	cfg   *Config
	cache = &CacheManager{items: make(map[string]CacheItem)}

	// lockMap 用于防止针对同一个目标 URL 并发启动多个 Mihomo 进程
	// 当多个请求同时请求同一个订阅链接时，保证只有一个请求去拉起 Mihomo
	lockMap sync.Map

	// fileWriteMu 用于防止并发写入规则文件导致文件损坏或数据交错
	fileWriteMu sync.Mutex
)

// debugLog 调试日志输出，仅在 Debug 模式开启时打印
func debugLog(format string, v ...interface{}) {
	if cfg.Debug {
		log.Printf("[DEBUG] "+format, v...)
	}
}

// maskLogURL 对日志中输出的订阅链接进行深度脱敏，隐藏包含密钥参数、用户名密码和敏感路径的部分
func maskLogURL(u string) string {
	// 如果包含管道符，说明是多个订阅链接组合，递归处理
	if strings.Contains(u, "|") {
		parts := strings.Split(u, "|")
		var masked []string
		for _, p := range parts {
			masked = append(masked, maskLogURL(p))
		}
		return strings.Join(masked, "|")
	}

	parsed, err := url.Parse(u)
	if err != nil {
		if len(u) > 50 {
			return u[:20] + "...(解析错误)"
		}
		return "***"
	}

	// 1. 隐藏可能存在的 HTTP Basic 鉴权信息 (如 http://user:pass@domain.com)
	if parsed.User != nil {
		parsed.User = url.UserPassword("***", "***")
	}

	// 2. 隐藏所有的查询参数 (Query，如 ?token=123&secret=abc)
	if parsed.RawQuery != "" {
		parsed.RawQuery = "***"
	}

	// 3. 隐藏 Fragment 片段 (如 #xxx)
	if parsed.Fragment != "" {
		parsed.Fragment = "***"
	}

	// 4. 对路径(Path)进行部分脱敏。许多服务商将 token 写在 path 中 (如 /sub/8a9b7c6d...)
	// 这里将超过 15 个字符的路径截断，保留前 10 个字符用于辨识
	if len(parsed.Path) > 15 {
		parsed.Path = parsed.Path[:10] + "...***"
	}

	return parsed.String()
}

// initConfig 初始化环境变量配置
func initConfig() {
	cfg = &Config{
		ListenAddr:       getEnv("LISTEN_ADDR", ":8080"),
		SubconverterURL:  getEnv("SUBCONVERTER_URL", "http://subconverter:25500"),
		MihomoPath:       getEnv("MIHOMO_PATH", "/usr/local/bin/mihomo"),
		ProxyPort:        getEnvAsInt("PROXY_PORT", 28080),
		ApiPort:          getEnvAsInt("API_PORT", 9090), // 新增: 默认 9090 作为 Mihomo 控制端 API 端口
		InternalBaseURL:  getEnv("INTERNAL_BASE_URL", "http://prefetch-proxy:8080"),
		Debug:            getEnv("DEBUG", "false") == "true",
		RuleListPath:     getEnv("RULE_LIST_PATH", ""),
		FakeIPFilterPath: getEnv("FAKE_IP_FILTER_PATH", ""),
		ChainToken:       getEnv("CHAIN_TOKEN", ""),
		PrivateNodesPath: getEnv("PRIVATE_NODES_PATH", "/private_nodes.yaml"),
	}
	domains := getEnv("TARGET_DOMAINS", "")
	if domains != "" {
		cfg.TargetDomains = strings.Split(domains, ",")
	}
}

func main() {
	initConfig()
	log.Printf("服务启动监听在 %s，Subconverter 后端: %s", cfg.ListenAddr, cfg.SubconverterURL)
	log.Printf("目标拦截域名: %v", cfg.TargetDomains)

	if cfg.RuleListPath != "" {
		log.Printf("已启用 Rule List 动态更新，路径: %s", cfg.RuleListPath)
	}
	if cfg.FakeIPFilterPath != "" {
		log.Printf("已启用 Fake-IP 模板动态更新，路径: %s", cfg.FakeIPFilterPath)
	}
	if cfg.ChainToken != "" {
		log.Printf("已启用私有节点注入，鉴权令牌已设为: %s", maskLogURL("token="+cfg.ChainToken))
	}
	if cfg.Debug {
		log.Printf("调试模式 (DEBUG) 已开启")
	}

	// 解析后端 Subconverter 的目标 URL
	targetURL, err := url.Parse(cfg.SubconverterURL)
	if err != nil {
		log.Fatalf("解析 Subconverter URL 失败: %v", err)
	}

	// 初始化针对 Subconverter 的反向代理
	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		// 关键修复：重写请求的 Host 头为后端的 Host，防止后端根据 Host 路由拒绝请求
		req.Host = targetURL.Host
	}

	mux := http.NewServeMux()

	// 优先匹配精确路径：处理私有节点链式代理注入的请求
	mux.HandleFunc("/internal/private", handlePrivateNodes)

	// 处理内部缓存请求短链，供 Subconverter 提取已经预获取好的节点数据
	mux.HandleFunc("/internal/", handleInternalSub)

	// 根路径拦截所有其他代理请求
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		handleProxyRequest(w, r, proxy)
	})

	server := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second, // 允许足够时间进行前置订阅和代理处理
	}

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("服务运行失败: %v", err)
	}
}

// handleProxyRequest 处理并重写客户端发往 Subconverter 的请求
func handleProxyRequest(w http.ResponseWriter, r *http.Request, proxy *httputil.ReverseProxy) {
	query := r.URL.Query()
	urlParam := query.Get("url")

	debugLog("收到代理请求, Path: %s", r.URL.Path)

	if urlParam == "" {
		debugLog("请求未包含 url 参数，直接转发给后端")
		proxy.ServeHTTP(w, r)
		return
	}

	debugLog("解析到的 url 参数值: %s", maskLogURL(urlParam))

	// 拆分多个由 "|" 分隔的订阅链接
	subURLs := strings.Split(urlParam, "|")
	var regularSubs []string
	modified := false

	// 遍历所有订阅链接，区分是否需要经过二次代理预获取
	for i, subURL := range subURLs {
		if isTargetDomain(subURL) {
			debugLog("命中目标域名，开始预获取流程: %s", maskLogURL(subURL))
			internalLink, err := processSubscription(subURL)
			if err != nil {
				log.Printf("处理二次订阅失败 [%s]: %v", maskLogURL(subURL), err)
				http.Error(w, fmt.Sprintf("代理预获取失败: %v", err), http.StatusInternalServerError)
				return
			}
			debugLog("订阅转换为内部链接: %s -> %s", maskLogURL(subURL), internalLink)
			// 将原始订阅链接替换为本地缓存服务的短链
			subURLs[i] = internalLink
			modified = true
		} else {
			debugLog("常规订阅记录（稍后将提取其域名）: %s", maskLogURL(subURL))
			regularSubs = append(regularSubs, subURL)
		}
	}

	// 链式代理：处理私有节点注入
	hasChainToken := query.Has("chaintoken")
	if cfg.ChainToken != "" && query.Get("chaintoken") == cfg.ChainToken {
		debugLog("匹配到正确的 chaintoken，注入私有节点")
		// 拼接通过接口暴露的本地域名链式节点提供给 Subconverter
		subURLs = append(subURLs, fmt.Sprintf("%s/internal/private", cfg.InternalBaseURL))
		modified = true
	}

	// 擦除 chaintoken 参数防止其透传给 Subconverter 产生不可预知的问题
	if hasChainToken {
		query.Del("chaintoken")
		modified = true
	}

	// 提取并写入常规订阅的域名（在后台解析写入）
	if len(regularSubs) > 0 {
		joinedRegularSubs := strings.Join(regularSubs, "|")
		processRegularSubscriptions(joinedRegularSubs)
	}

	// 如果参数发生了改变，重写 HTTP 请求参数
	if modified {
		query.Set("url", strings.Join(subURLs, "|"))
		r.URL.RawQuery = query.Encode()
		log.Printf("请求参数已修改发往 Subconverter (包含内部获取缓存链接或私有节点)")
	} else {
		debugLog("未修改任何订阅链接，原样转发")
	}

	// 将(可能修改过的)请求移交给内置的反向代理，发送给真实的 Subconverter
	proxy.ServeHTTP(w, r)
}

// processRegularSubscriptions 处理不需要二次代理的常规订阅
// 该函数利用后端的 Subconverter 预先将其转换为 Clash 格式，以便提取节点域名并写入规则文件
func processRegularSubscriptions(joinedSubs string) {
	if cfg.RuleListPath == "" && cfg.FakeIPFilterPath == "" {
		// 如果用户没配置需要写入规则文件，就没必要进行预解析了
		return
	}

	hash := md5Hash("regular_" + joinedSubs)
	// 如果缓存中已经存在，说明最近解析过，避免频繁请求后端
	if cache.Get(hash) != nil {
		debugLog("常规订阅组合已预解析过，跳过域名提取")
		return
	}

	var mtx sync.Mutex
	v, _ := lockMap.LoadOrStore(hash, &mtx)
	mu := v.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	// 获得锁后二次检查缓存 (Double-check locking)
	if cache.Get(hash) != nil {
		return
	}

	log.Printf("开始预解析常规订阅以提取域名...")

	// 构造向后端的 Subconverter 请求，强制 target=clash 以便解析 yaml
	baseURL := strings.TrimRight(cfg.SubconverterURL, "/")
	parseURL := fmt.Sprintf("%s/sub?target=clash&url=%s", baseURL, url.QueryEscape(joinedSubs))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", parseURL, nil)
	req.Header.Set("User-Agent", "ClashforWindows/0.19.23")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("请求 Subconverter 解析常规订阅失败: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		body, err := io.ReadAll(resp.Body)
		if err == nil {
			// 同步提取并写入规则文件
			updateRuleFiles(body)
			// 设置 1 小时缓存标记，避免短期内对完全相同的常规订阅组合重复请求解析
			cache.Set(hash, []byte("processed"), 1*time.Hour)
			log.Printf("常规订阅域名提取完成")
		}
	} else {
		log.Printf("解析常规订阅时 Subconverter 返回非 200 状态码: %d", resp.StatusCode)
	}
}

// processSubscription 执行二次代理（预获取）的核心逻辑
// 1. 直连获取前置节点 -> 2. 本地拉起 Mihomo -> 3. 通过 Mihomo 走前置节点获取真实订阅 -> 4. 提取域名并缓存结果
func processSubscription(targetURL string) (string, error) {
	hash := md5Hash(targetURL)
	debugLog("处理订阅 URL 哈希值: %s", hash)

	// 1. 检查缓存是否命中
	if cache.Get(hash) != nil {
		log.Printf("命中缓存: %s", maskLogURL(targetURL))
		return fmt.Sprintf("%s/internal/%s", cfg.InternalBaseURL, hash), nil
	}

	// 使用 Mutex 防止对同一 URL 并发启动多个 Mihomo 进程
	var mtx sync.Mutex
	v, _ := lockMap.LoadOrStore(hash, &mtx)
	mu := v.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	// 获得锁后二次检查缓存 (Double-check locking)
	if cache.Get(hash) != nil {
		return fmt.Sprintf("%s/internal/%s", cfg.InternalBaseURL, hash), nil
	}

	log.Printf("开始预获取前置节点: %s", maskLogURL(targetURL))

	// 2. 获取前置节点（未翻墙环境获取的原始订阅节点）
	preNodes, err := fetchPreNodes(targetURL)
	if err != nil {
		return "", err
	}

	// 3. 启动临时 Mihomo 代理（提取节点名称并采用 Select 手动策略组）
	cmd, tempFilePath, nodeNames, err := startTempProxy(cfg, preNodes)
	if err != nil {
		return "", err
	}
	// 确保不论发生什么错误，都会彻底清理衍生的 Mihomo 进程和临时配置文件
	defer func() {
		if cmd != nil && cmd.Process != nil {
			debugLog("关闭临时 Mihomo 进程 PID: %d", cmd.Process.Pid)
			cmd.Process.Kill()
			cmd.Wait()
		}
		if tempFilePath != "" {
			os.Remove(tempFilePath)
		}
	}()

	// 4. 获取真实节点 (配合显式 API 节点切换重试机制)
	log.Printf("通过本地 SOCKS5 获取真实节点...")
	// 设定重试次数为3，传入解析到的前置节点名称列表
	realData, err := fetchRealNodesWithRetry(targetURL, cfg.ProxyPort, cfg.ApiPort, nodeNames, 3)
	if err != nil {
		return "", err
	}

	// 5. 等待真实节点中的域名提取并写入本地规则文件
	// 重要：这里必须是同步阻塞调用，确保提取完成后再将订阅移交给 Subconverter
	updateRuleFiles(realData)

	// 6. 将成功获取的真实节点数据写入缓存 (TTL: 1小时)
	debugLog("将真实节点数据写入缓存，设置过期时间为 1 小时")
	cache.Set(hash, realData, 1*time.Hour)

	// 返回内部短链，Subconverter 接下来将访问此链接获取缓存数据
	return fmt.Sprintf("%s/internal/%s", cfg.InternalBaseURL, hash), nil
}

// updateRuleFiles 解析获得的节点 YAML 数据，提取真实域名并同步更新到指定文件
func updateRuleFiles(realData []byte) {
	if cfg.RuleListPath == "" && cfg.FakeIPFilterPath == "" {
		return // 未配置文件路径，不执行操作
	}

	var config ClashConfig
	err := yaml.Unmarshal(realData, &config)
	if err != nil {
		log.Printf("更新规则文件失败，解析 YAML 错误: %v", err)
		return
	}

	var domains []string
	domainMap := make(map[string]bool)

	// 遍历所有节点，提取 "server" 字段
	for _, proxy := range config.Proxies {
		if server, ok := proxy["server"].(string); ok {
			// 过滤出真正的域名，排除掉 IPv4 / IPv6
			if isDomain(server) && !domainMap[server] {
				domains = append(domains, server)
				domainMap[server] = true
			}
		}
	}

	if len(domains) == 0 {
		debugLog("未提取到任何节点域名，跳过更新规则文件")
		return
	}

	// 使用全局互斥锁保证文件写入的原子性和安全性，避免写坏文件
	fileWriteMu.Lock()
	defer fileWriteMu.Unlock()

	// 写入 Rule List (追加模式)
	if cfg.RuleListPath != "" {
		appendToFileUnique(cfg.RuleListPath, domains, func(content string) string {
			return "DOMAIN-SUFFIX,"
		})
	}

	// 写入 Fake-IP Filter 模板 (带智能缩进检测，适配用户的原有排版)
	if cfg.FakeIPFilterPath != "" {
		appendToFileUnique(cfg.FakeIPFilterPath, domains, func(content string) string {
			// 扫描文件找出用户自用的数组缩进习惯
			lines := strings.Split(content, "\n")
			for _, line := range lines {
				// 寻找类似于 "- " 或 "  - " 的列表结构
				idx := strings.Index(line, "- ")
				if idx != -1 {
					// 确保前置的字符全都是空格或是空字符串
					if strings.TrimSpace(line[:idx]) == "" {
						// 按照用户的缩进加上 "+." 构建格式
						return line[:idx] + "- +."
					}
				}
			}
			// 如果是空文件或者没有找到标准列表，则默认无前置空格格式
			return "- +."
		})
	}
}

// isDomain 检查传入的地址是否为域名 (粗略判断：无法被解析为合法 IP 即认为是域名)
func isDomain(address string) bool {
	if address == "" {
		return false
	}
	return net.ParseIP(address) == nil
}

// appendToFileUnique 检查行是否存在，不存在则追加到文件中，保证文件内容唯一不重复
func appendToFileUnique(filePath string, domains []string, detectPrefixFunc func(string) string) {
	content, err := os.ReadFile(filePath)
	if err != nil && !os.IsNotExist(err) {
		log.Printf("读取文件失败 %s: %v", filePath, err)
		return
	}

	// 动态检测文件中对应的格式前缀（缩进等）
	prefix := detectPrefixFunc(string(content))

	// 用 HashSet 记录当前文件中已存在的行，防止重复添加
	existingMap := make(map[string]bool)
	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			existingMap[trimmed] = true
		}
	}

	var linesToAdd []string
	for _, domain := range domains {
		lineStr := prefix + domain
		trimmed := strings.TrimSpace(lineStr) // 去除空白用于比对唯一性
		if !existingMap[trimmed] {
			linesToAdd = append(linesToAdd, lineStr) // 追加时使用带有计算好的格式/缩进原文
			existingMap[trimmed] = true              // 防止单次处理产生的多个重复记录
		}
	}

	if len(linesToAdd) == 0 {
		return // 所有域名都已存在，无需更新
	}

	// 打开文件追加模式
	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("打开文件失败 %s: %v", filePath, err)
		return
	}
	defer f.Close()

	// 若文件非空，且结尾没有换行符，则先补齐换行符保证格式不被破坏
	if len(content) > 0 && content[len(content)-1] != '\n' {
		f.WriteString("\n")
	}

	// 写入新行
	for _, line := range linesToAdd {
		f.WriteString(line + "\n")
	}
	log.Printf("已向文件 %s 追加了 %d 条新规则", filePath, len(linesToAdd))
}

// fetchPreNodes 获取并提取所有前置节点 (未经过代理)
func fetchPreNodes(targetURL string) ([]Node, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	debugLog("fetchPreNodes: 开始请求 %s", maskLogURL(targetURL))
	req, _ := http.NewRequestWithContext(ctx, "GET", targetURL, nil)
	req.Header.Set("User-Agent", "ClashforWindows/0.19.23") // 伪装 UA 防止被简单反扒拦截

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("获取前置订阅失败: %w", err)
	}
	defer resp.Body.Close()

	debugLog("fetchPreNodes: HTTP 状态码: %d", resp.StatusCode)

	body, _ := io.ReadAll(resp.Body)
	debugLog("fetchPreNodes: 获取到响应体，长度: %d 字节", len(body))

	var config ClashConfig
	err = yaml.Unmarshal(body, &config)
	if err != nil {
		return nil, fmt.Errorf("解析 YAML 失败: %w", err)
	}

	if len(config.Proxies) == 0 {
		return nil, fmt.Errorf("未找到任何前置节点")
	}

	debugLog("fetchPreNodes: 成功解析出 %d 个前置节点", len(config.Proxies))
	return config.Proxies, nil
}

// startTempProxy 生成包含 external-controller 控制端的 Mihomo 配置文件，并作为子进程启动
func startTempProxy(cfg *Config, preNodes []Node) (*exec.Cmd, string, []string, error) {
	var nodeNames []string
	for _, n := range preNodes {
		if name, ok := n["name"].(string); ok {
			nodeNames = append(nodeNames, name)
		}
	}

	// 构造临时 Mihomo 代理配置
	// 使用 rule 模式配合 select 策略组，由外部代码通过 API 指挥切换节点
	tempConfig := map[string]interface{}{
		"socks-port":          cfg.ProxyPort,
		"allow-lan":           false,
		"mode":                "rule",
		"log-level":           "warning",
		"external-controller": fmt.Sprintf("127.0.0.1:%d", cfg.ApiPort), // 新增：暴露 API 控制端
		"proxies":             preNodes,
		"proxy-groups": []map[string]interface{}{
			{
				"name":    "Select-Proxy",
				"type":    "select", // 更改为 select 类型，准备接受 API 控制
				"proxies": nodeNames,
			},
		},
		"rules": []string{
			"MATCH,Select-Proxy", // 将所有流量强制路由到 Select 组
		},
	}

	configBytes, err := yaml.Marshal(tempConfig)
	if err != nil {
		return nil, "", nil, err
	}

	if cfg.Debug {
		debugLog("startTempProxy: 临时 Mihomo 配置已生成，启用了 API 控制端 (端口: %d)，包含 %d 个节点", cfg.ApiPort, len(nodeNames))
	}

	// 建立系统临时文件写入配置文件
	tempFile, err := os.CreateTemp("", "mihomo_temp_*.yaml")
	if err != nil {
		return nil, "", nil, err
	}
	tempFilePath := tempFile.Name()

	if _, err := tempFile.Write(configBytes); err != nil {
		tempFile.Close()
		os.Remove(tempFilePath)
		return nil, "", nil, err
	}
	tempFile.Close()

	// 启动 Mihomo 子进程
	cmd := exec.Command(cfg.MihomoPath, "-f", tempFilePath)

	// 在调试模式下将 Mihomo 日志直接打到主进程控制台
	if cfg.Debug {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	} else {
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
	}

	if err := cmd.Start(); err != nil {
		os.Remove(tempFilePath)
		return nil, "", nil, fmt.Errorf("启动 Mihomo 失败: %w", err)
	}

	// 给予内核启动和建立 API 服务缓冲时间
	log.Printf("Mihomo 引擎和 API 接口正在启动...")
	time.Sleep(2 * time.Second)

	// 返回提取出的 nodeNames 列表，方便在重试时利用 API 指定节点
	return cmd, tempFilePath, nodeNames, nil
}

// selectMihomoNode 通过 Mihomo 的 REST API 手动切换指定策略组到给定的目标节点
func selectMihomoNode(apiPort int, groupName, nodeName string) error {
	apiUrl := fmt.Sprintf("http://127.0.0.1:%d/proxies/%s", apiPort, url.PathEscape(groupName))

	// 构造切换节点的 JSON 请求体
	payloadData := map[string]string{"name": nodeName}
	jsonData, err := json.Marshal(payloadData)
	if err != nil {
		return fmt.Errorf("JSON 序列化失败: %w", err)
	}

	req, err := http.NewRequest("PUT", apiUrl, bytes.NewReader(jsonData))
	if err != nil {
		return fmt.Errorf("创建 API 请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("API 请求执行失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("返回异常状态码 %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// fetchRealNodesWithRetry 通过本地 Mihomo 代理获取真实节点数据
// 新增逻辑：遍历 nodeNames 列表，使用 API 显式要求 Mihomo 切换前置节点进行重试
func fetchRealNodesWithRetry(targetURL string, proxyPort int, apiPort int, nodeNames []string, maxRetries int) ([]byte, error) {
	proxyStr := fmt.Sprintf("socks5://127.0.0.1:%d", proxyPort)
	proxyURL, _ := url.Parse(proxyStr)

	transport := &http.Transport{
		Proxy: http.ProxyURL(proxyURL),
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   15 * time.Second, // 设置单次 HTTP 请求合理的超时时间
	}

	var lastErr error

	// 如果前置节点总数小于设定的重试次数，则最多尝试所有节点一遍即可
	limit := maxRetries
	if len(nodeNames) < limit {
		limit = len(nodeNames)
	}

	// 循环执行请求重试，并主动分配节点
	for attempt := 0; attempt < limit; attempt++ {
		// 挑选本轮重试的前置节点
		currentNode := nodeNames[attempt]
		log.Printf("fetchRealNodes: [尝试 %d/%d] 正在通过 API 选择前置节点: %s", attempt+1, limit, currentNode)

		// 调用 API 切换 Mihomo 策略
		err := selectMihomoNode(apiPort, "Select-Proxy", currentNode)
		if err != nil {
			lastErr = fmt.Errorf("API 切换节点 [%s] 失败: %w", currentNode, err)
			log.Printf("fetchRealNodes: %v", lastErr)
			continue
		}

		// 给予 Mihomo 缓冲时间应用新的节点链路
		time.Sleep(500 * time.Millisecond)

		debugLog("fetchRealNodes: 发起 SOCKS5 代理请求: %s", maskLogURL(targetURL))

		req, _ := http.NewRequest("GET", targetURL, nil)
		req.Header.Set("User-Agent", "ClashforWindows/0.19.23")

		resp, err := client.Do(req)

		// 如果请求报错 (通常是代理节点离线、连接拒接或超时)
		if err != nil {
			lastErr = fmt.Errorf("节点 [%s] 请求失败: %w", currentNode, err)
			log.Printf("fetchRealNodes: 前置节点 [%s] 失败，准备尝试下一个", currentNode)
			continue
		}

		// 如果状态码不是 200 OK
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastErr = fmt.Errorf("节点 [%s] 请求返回非 200 状态码: %d", currentNode, resp.StatusCode)
			log.Printf("fetchRealNodes: 前置节点 [%s] 获取失败(HTTP %d)，准备尝试下一个", currentNode, resp.StatusCode)
			continue
		}

		// 成功返回 200 OK，读取并返回内容
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()

		if readErr == nil {
			log.Printf("fetchRealNodes: 前置节点 [%s] 成功获取真实订阅数据！长度: %d 字节", currentNode, len(body))
			return body, nil
		} else {
			lastErr = fmt.Errorf("读取节点 [%s] 响应体失败: %w", currentNode, readErr)
		}
	}

	return nil, fmt.Errorf("获取真实节点失败，已尝试完设定的节点数 (%d)，最后一次错误: %v", limit, lastErr)
}

// handlePrivateNodes 响应 Subconverter 获取私有节点配置的请求
// 将保存在本地磁盘上的私有节点注入到订阅中
func handlePrivateNodes(w http.ResponseWriter, r *http.Request) {
	if cfg.PrivateNodesPath == "" {
		http.Error(w, "未配置私有节点文件路径", http.StatusNotFound)
		return
	}

	data, err := os.ReadFile(cfg.PrivateNodesPath)
	if err != nil {
		debugLog("读取私有节点文件失败: %v", err)
		http.Error(w, "Failed to read private nodes", http.StatusInternalServerError)
		return
	}

	var config ClashConfig
	err = yaml.Unmarshal(data, &config)
	if err != nil {
		debugLog("解析私有节点 YAML 失败: %v", err)
		http.Error(w, "Failed to unmarshal private nodes", http.StatusInternalServerError)
		return
	}

	for i := range config.Proxies {
		// 给私有节点增加醒目的前缀
		if name, ok := config.Proxies[i]["name"].(string); ok {
			if !strings.HasPrefix(name, "🔒私有") {
				config.Proxies[i]["name"] = "🔒私有 - " + name
			}
		}
		// 动态注入 dialer-proxy 字段，作为链式代理第二跳的基础配置
		config.Proxies[i]["dialer-proxy"] = "🚀 前置节点池"
	}

	outData, err := yaml.Marshal(&config)
	if err != nil {
		debugLog("序列化私有节点失败: %v", err)
		http.Error(w, "Failed to marshal private nodes", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(outData)
}

// handleInternalSub 响应 Subconverter 请求本服务获取已预提取和缓存好的订阅配置
func handleInternalSub(w http.ResponseWriter, r *http.Request) {
	hash := strings.TrimPrefix(r.URL.Path, "/internal/")
	if hash == "" {
		http.Error(w, "无效的请求", http.StatusBadRequest)
		return
	}

	debugLog("Subconverter 正在拉取内部缓存，哈希: %s", hash)

	// 从缓存读取已获取的节点数据
	data := cache.Get(hash)
	if data == nil {
		debugLog("请求的缓存未找到或已过期: %s", hash)
		http.Error(w, "订阅缓存已过期或不存在", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(data)
}

// --- 辅助函数 ---

// isTargetDomain 校验给定的订阅链接是否匹配我们配置的目标域名
func isTargetDomain(u string) bool {
	parsedURL, err := url.Parse(u)
	host := ""
	if err == nil {
		host = parsedURL.Hostname()
	}

	// 此处检查域名前不需要执行脱敏逻辑，以确保精准匹配
	for _, domain := range cfg.TargetDomains {
		// 1. 标准 Hostname 匹配 (例如 https://api.example.com/sub)
		if host != "" && strings.Contains(host, domain) {
			debugLog("isTargetDomain 命中 [Hostname匹配]: %s 包含 %s", host, domain)
			return true
		}
		// 2. 兜底匹配：当链接缺少协议(http/https)导致 Hostname 解析为空时，直接在原始字符串中匹配
		if host == "" && strings.Contains(u, domain) {
			debugLog("isTargetDomain 命中 [字符串兜底匹配]: %s 包含 %s", maskLogURL(u), domain)
			return true
		}
	}

	return false
}

// md5Hash 简单的计算 MD5 字符串
func md5Hash(text string) string {
	hasher := md5.New()
	hasher.Write([]byte(text))
	return hex.EncodeToString(hasher.Sum(nil))
}

// getEnv 读取环境变量，如果不存在则返回 fallback 默认值
func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}

// getEnvAsInt 读取环境变量并转换为整型，如果失败或不存在则返回 fallback 默认值
func getEnvAsInt(key string, fallback int) int {
	valStr := getEnv(key, "")
	if valStr == "" {
		return fallback
	}
	var val int
	fmt.Sscanf(valStr, "%d", &val)
	return val
}

// --- 缓存管理器实现 ---

// Set 写入缓存
func (c *CacheManager) Set(key string, data []byte, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[key] = CacheItem{
		Data:      data,
		ExpiresAt: time.Now().Add(ttl),
	}
}

// Get 读取缓存，若缓存已过期则自动失效并返回 nil
func (c *CacheManager) Get(key string) []byte {
	c.mu.RLock()
	defer c.mu.RUnlock()
	item, found := c.items[key]
	if !found {
		return nil
	}
	// 如果当前时间已经超过了设定的过期时间
	if time.Now().After(item.ExpiresAt) {
		return nil
	}
	return item.Data
}
