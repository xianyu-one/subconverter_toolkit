package main

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
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

// Config 环境配置结构
type Config struct {
	ListenAddr       string
	SubconverterURL  string
	TargetDomains    []string
	MihomoPath       string
	ProxyPort        int
	InternalBaseURL  string // 用于 Subconverter 访问本服务缓存的内部地址
	Debug            bool   // 是否开启调试日志
	RuleListPath     string // 规则集文件路径
	FakeIPFilterPath string // Fake-IP 模板文件路径
}

type Node map[string]interface{}

type ClashConfig struct {
	Proxies []Node `yaml:"proxies"`
}

// CacheItem 缓存数据项
type CacheItem struct {
	Data      []byte
	ExpiresAt time.Time
}

// CacheManager 简单的并发安全内存缓存
type CacheManager struct {
	mu    sync.RWMutex
	items map[string]CacheItem
}

var (
	cfg   *Config
	cache = &CacheManager{items: make(map[string]CacheItem)}
	// lockMap 用于防止针对同一个目标 URL 并发启动多个 Mihomo 进程
	lockMap sync.Map
	// fileWriteMu 用于防止并发写入规则文件导致文件损坏或数据交错
	fileWriteMu sync.Mutex
)

// debugLog 调试日志输出
func debugLog(format string, v ...interface{}) {
	if cfg.Debug {
		log.Printf("[DEBUG] "+format, v...)
	}
}

// maskLogURL 对日志中输出的订阅链接进行脱敏，隐藏包含密钥参数的部分
func maskLogURL(u string) string {
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
	if parsed.RawQuery != "" {
		parsed.RawQuery = "***" // 隐藏 Query 参数，如 token、secret
	}
	return parsed.String()
}

func initConfig() {
	cfg = &Config{
		ListenAddr:       getEnv("LISTEN_ADDR", ":8080"),
		SubconverterURL:  getEnv("SUBCONVERTER_URL", "http://subconverter:25500"),
		MihomoPath:       getEnv("MIHOMO_PATH", "/usr/local/bin/mihomo"),
		ProxyPort:        getEnvAsInt("PROXY_PORT", 28080),
		InternalBaseURL:  getEnv("INTERNAL_BASE_URL", "http://prefetch-proxy:8080"),
		Debug:            getEnv("DEBUG", "false") == "true",
		RuleListPath:     getEnv("RULE_LIST_PATH", ""),
		FakeIPFilterPath: getEnv("FAKE_IP_FILTER_PATH", ""),
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
	if cfg.Debug {
		log.Printf("调试模式 (DEBUG) 已开启")
	}

	// 解析 Subconverter 的目标 URL
	targetURL, err := url.Parse(cfg.SubconverterURL)
	if err != nil {
		log.Fatalf("解析 Subconverter URL 失败: %v", err)
	}

	// 初始化反向代理
	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		// 关键：重写 Host，防止后端路由拒绝
		req.Host = targetURL.Host
	}

	mux := http.NewServeMux()

	// 处理内部缓存请求短链
	mux.HandleFunc("/internal/", handleInternalSub)

	// 拦截所有其他请求
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

// handleProxyRequest 处理并重写客户端请求
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

	// 拆分多个订阅链接
	subURLs := strings.Split(urlParam, "|")
	var regularSubs []string
	modified := false

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
			subURLs[i] = internalLink
			modified = true
		} else {
			debugLog("常规订阅记录（将提取其域名）: %s", maskLogURL(subURL))
			regularSubs = append(regularSubs, subURL)
		}
	}

	// [新增] 提取并写入常规订阅的域名
	if len(regularSubs) > 0 {
		joinedRegularSubs := strings.Join(regularSubs, "|")
		processRegularSubscriptions(joinedRegularSubs)
	}

	if modified {
		query.Set("url", strings.Join(subURLs, "|"))
		r.URL.RawQuery = query.Encode()
		log.Printf("请求参数已修改发往 Subconverter (包含内部获取缓存链接)")
	} else {
		debugLog("未修改任何订阅链接，原样转发")
	}

	proxy.ServeHTTP(w, r)
}

// processRegularSubscriptions 处理不需要二次代理的常规订阅，
// 利用后端的 Subconverter 预先将其转换为 Clash 格式，以便我们提取节点域名并写入规则文件
func processRegularSubscriptions(joinedSubs string) {
	if cfg.RuleListPath == "" && cfg.FakeIPFilterPath == "" {
		// 如果用户没配置需要写入规则文件，就没必要进行预解析了
		return
	}

	hash := md5Hash("regular_" + joinedSubs)
	if cache.Get(hash) != nil {
		debugLog("常规订阅组合已预解析过，跳过域名提取")
		return
	}

	var mtx sync.Mutex
	v, _ := lockMap.LoadOrStore(hash, &mtx)
	mu := v.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	// 二次检查缓存
	if cache.Get(hash) != nil {
		return
	}

	log.Printf("开始预解析常规订阅以提取域名...")

	// 构造向后端的 Subconverter 请求，强制转换为 clash 格式以便解析 yaml
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
			// 缓存标记，避免短期内重复请求解析（设置 1 小时缓存）
			cache.Set(hash, []byte("processed"), 1*time.Hour)
			log.Printf("常规订阅域名提取完成")
		}
	} else {
		log.Printf("解析常规订阅时 Subconverter 返回非 200 状态码: %d", resp.StatusCode)
	}
}

// processSubscription 执行二次代理两步走订阅获取逻辑
func processSubscription(targetURL string) (string, error) {
	hash := md5Hash(targetURL)
	debugLog("处理订阅 URL 哈希值: %s", hash)

	// 1. 检查缓存
	if cache.Get(hash) != nil {
		log.Printf("命中缓存: %s", maskLogURL(targetURL))
		return fmt.Sprintf("%s/internal/%s", cfg.InternalBaseURL, hash), nil
	}

	// 使用 Mutex 防止同一 URL 并发启动 Mihomo
	var mtx sync.Mutex
	v, _ := lockMap.LoadOrStore(hash, &mtx)
	mu := v.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	// 二次检查缓存 (Double-check locking)
	if cache.Get(hash) != nil {
		return fmt.Sprintf("%s/internal/%s", cfg.InternalBaseURL, hash), nil
	}

	log.Printf("开始预获取前置节点: %s", maskLogURL(targetURL))

	// 2. 获取前置节点
	preNodes, err := fetchPreNodes(targetURL)
	if err != nil {
		return "", err
	}

	// 3. 启动临时 Mihomo 代理
	cmd, tempFilePath, err := startTempProxy(cfg, preNodes)
	if err != nil {
		return "", err
	}
	// 确保彻底清理进程和配置文件
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

	// 4. 获取真实节点
	log.Printf("通过本地 SOCKS5 获取真实节点...")
	realData, err := fetchRealNodes(targetURL, cfg.ProxyPort)
	if err != nil {
		return "", err
	}

	// 重要：这里必须是同步阻塞调用！
	// 等待真实节点中的域名提取并写入本地文件后，再将请求移交给 Subconverter，
	updateRuleFiles(realData)

	// 5. 写入缓存 (TTL: 1小时)
	debugLog("将真实节点数据写入缓存，设置过期时间为 1 小时")
	cache.Set(hash, realData, 1*time.Hour)

	// 6. 返回内部短链
	return fmt.Sprintf("%s/internal/%s", cfg.InternalBaseURL, hash), nil
}

// updateRuleFiles 解析真实节点文件，提取域名并同步更新到指定的文件
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

	// 使用互斥锁保证文件写入的原子性和安全性
	fileWriteMu.Lock()
	defer fileWriteMu.Unlock()

	// 写入 Rule List
	if cfg.RuleListPath != "" {
		appendToFileUnique(cfg.RuleListPath, domains, func(content string) string {
			return "DOMAIN-SUFFIX,"
		})
	}

	// 写入 Fake-IP Filter 模板 (带智能缩进检测)
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

// appendToFileUnique 检查行是否存在，不存在则追加到文件中
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
		return
	}

	// 打开文件追加模式
	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("打开文件失败 %s: %v", filePath, err)
		return
	}
	defer f.Close()

	// 若文件非空，且结尾没有换行符，则先补齐换行符保证格式
	if len(content) > 0 && content[len(content)-1] != '\n' {
		f.WriteString("\n")
	}

	// 写入新行
	for _, line := range linesToAdd {
		f.WriteString(line + "\n")
	}
	log.Printf("已向文件 %s 追加了 %d 条新规则", filePath, len(linesToAdd))
}

// fetchPreNodes 获取并提取所有前置节点
func fetchPreNodes(targetURL string) ([]Node, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	debugLog("fetchPreNodes: 开始请求 %s", maskLogURL(targetURL))
	req, _ := http.NewRequestWithContext(ctx, "GET", targetURL, nil)
	req.Header.Set("User-Agent", "ClashforWindows/0.19.23") // 伪装 UA 以防被墙

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

// startTempProxy 生成带故障切换的 Mihomo 配置并启动
func startTempProxy(cfg *Config, preNodes []Node) (*exec.Cmd, string, error) {
	var nodeNames []string
	for _, n := range preNodes {
		if name, ok := n["name"].(string); ok {
			nodeNames = append(nodeNames, name)
		}
	}

	// 构造带有 fallback 策略的配置 (使用 rule 模式确保流量被接管)
	tempConfig := map[string]interface{}{
		"socks-port": cfg.ProxyPort,
		"allow-lan":  false,
		"mode":       "rule",
		"log-level":  "warning",
		"proxies":    preNodes,
		"proxy-groups": []map[string]interface{}{
			{
				"name":     "Fallback-Proxy",
				"type":     "fallback",
				"proxies":  nodeNames,
				"url":      "http://www.gstatic.com/generate_204",
				"interval": 300,
				"timeout":  5000,
			},
		},
		"rules": []string{
			"MATCH,Fallback-Proxy", // 所有流量走 fallback 组
		},
	}

	configBytes, err := yaml.Marshal(tempConfig)
	if err != nil {
		return nil, "", err
	}

	if cfg.Debug {
		debugLog("startTempProxy: 临时 Mihomo 配置内容:\n%s", string(configBytes))
	}

	tempFile, err := os.CreateTemp("", "mihomo_temp_*.yaml")
	if err != nil {
		return nil, "", err
	}
	tempFilePath := tempFile.Name()

	if _, err := tempFile.Write(configBytes); err != nil {
		tempFile.Close()
		os.Remove(tempFilePath)
		return nil, "", err
	}
	tempFile.Close()

	// 启动进程
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
		return nil, "", fmt.Errorf("启动 Mihomo 失败: %w", err)
	}

	// 给予内核启动和执行 initial 测速(fallback)的时间
	log.Printf("Mihomo 已启动，等待 4 秒进行并发测速...")
	time.Sleep(4 * time.Second)

	return cmd, tempFilePath, nil
}

// fetchRealNodes 通过代理获取真实节点数据
func fetchRealNodes(targetURL string, proxyPort int) ([]byte, error) {
	proxyStr := fmt.Sprintf("socks5://127.0.0.1:%d", proxyPort)
	proxyURL, _ := url.Parse(proxyStr)

	debugLog("fetchRealNodes: 准备通过代理 (%s) 请求目标: %s", proxyStr, maskLogURL(targetURL))

	transport := &http.Transport{
		Proxy: http.ProxyURL(proxyURL),
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   20 * time.Second, // 代理请求设置适当的超时
	}

	req, _ := http.NewRequest("GET", targetURL, nil)
	req.Header.Set("User-Agent", "ClashforWindows/0.19.23")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("代理请求目标失败: %w", err)
	}
	defer resp.Body.Close()

	debugLog("fetchRealNodes: 请求完成，HTTP 状态码: %d", resp.StatusCode)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("代理请求返回非 200 状态码: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err == nil {
		debugLog("fetchRealNodes: 成功获取真实节点数据，长度: %d 字节", len(body))
	}
	return body, err
}

// handleInternalSub 响应 Subconverter 获取缓存订阅的请求
func handleInternalSub(w http.ResponseWriter, r *http.Request) {
	hash := strings.TrimPrefix(r.URL.Path, "/internal/")
	if hash == "" {
		http.Error(w, "无效的请求", http.StatusBadRequest)
		return
	}

	debugLog("Subconverter 正在拉取内部缓存，哈希: %s", hash)

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

func isTargetDomain(u string) bool {
	parsedURL, err := url.Parse(u)
	host := ""
	if err == nil {
		host = parsedURL.Hostname()
	}

	// 检查域名前不需要脱敏，避免影响排障
	for _, domain := range cfg.TargetDomains {
		// 1. 标准 Hostname 匹配
		if host != "" && strings.Contains(host, domain) {
			debugLog("isTargetDomain 命中 [Hostname匹配]: %s 包含 %s", host, domain)
			return true
		}
		// 2. 兜底匹配：当缺少协议导致 Hostname 解析为空时，直接匹配字符串
		if host == "" && strings.Contains(u, domain) {
			debugLog("isTargetDomain 命中 [字符串兜底匹配]: %s 包含 %s", maskLogURL(u), domain)
			return true
		}
	}

	return false
}

func md5Hash(text string) string {
	hasher := md5.New()
	hasher.Write([]byte(text))
	return hex.EncodeToString(hasher.Sum(nil))
}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}

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

func (c *CacheManager) Set(key string, data []byte, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[key] = CacheItem{
		Data:      data,
		ExpiresAt: time.Now().Add(ttl),
	}
}

func (c *CacheManager) Get(key string) []byte {
	c.mu.RLock()
	defer c.mu.RUnlock()
	item, found := c.items[key]
	if !found {
		return nil
	}
	if time.Now().After(item.ExpiresAt) {
		return nil
	}
	return item.Data
}
