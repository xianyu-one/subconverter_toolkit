package app

import (
	"log"
	"net/url"
	"strings"
	"sync"
	"time"

	"prefetch-proxy/internal/prefetch"
	"prefetch-proxy/internal/privateconfig"
	"prefetch-proxy/internal/rules"
)

// Config 环境配置结构，存放所有从环境变量读取的配置项
type Config struct {
	ListenAddr        string   // 服务监听地址，默认 :8080
	SubconverterURL   string   // 后端真实的 Subconverter 地址
	TargetDomains     []string // 需要被拦截并执行二次代理（预获取）的目标域名列表
	MihomoPath        string   // Mihomo 可执行文件的绝对路径
	ProxyPort         int      // 临时拉起的 Mihomo 提供的 Socks5 代理端口
	ApiPort           int      // 临时拉起的 Mihomo 提供的 External Controller API 控制端口
	InternalBaseURL   string   // 用于 Subconverter 访问本服务缓存的内部地址
	Debug             bool     // 是否开启调试日志输出
	RuleListPath      string   // 提取真实节点域名后，写入的规则集文件路径
	FakeIPFilterPath  string   // 提取真实节点域名后，写入的 Fake-IP 模板文件路径
	PrivateConfigPath string   // 包含私有节点、组和密钥的 YAML 路径
}

type Service struct {
	cfg          *Config
	cache        *CacheManager
	privateNodes *privateconfig.Config
	privateLinks *privateconfig.LinkStore
	lockMap      sync.Map
	rules        *rules.Updater
	prefetch     *prefetch.Client
}

func NewService(cfg *Config, privateNodes *privateconfig.Config) *Service {
	s := &Service{
		cfg:          cfg,
		cache:        &CacheManager{items: make(map[string]CacheItem)},
		privateNodes: privateNodes,
		privateLinks: privateconfig.NewLinkStore(10*time.Minute, time.Now),
		rules:        &rules.Updater{RuleListPath: cfg.RuleListPath, FakeIPFilterPath: cfg.FakeIPFilterPath, Debug: cfg.Debug},
	}
	s.prefetch = prefetch.NewClient(prefetch.Config{MihomoPath: cfg.MihomoPath, ProxyPort: cfg.ProxyPort, ApiPort: cfg.ApiPort, Debug: cfg.Debug}, s.debugLog, maskLogURL)
	return s
}

// debugLog 调试日志输出，仅在 Debug 模式开启时打印
func (s *Service) debugLog(format string, v ...interface{}) {
	if s.cfg.Debug {
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
func loadConfig() (*Config, *privateconfig.Config, error) {
	cfg := &Config{
		ListenAddr:        getEnv("LISTEN_ADDR", ":8080"),
		SubconverterURL:   getEnv("SUBCONVERTER_URL", "http://subconverter:25500"),
		MihomoPath:        getEnv("MIHOMO_PATH", "/usr/local/bin/mihomo"),
		ProxyPort:         getEnvAsInt("PROXY_PORT", 28080),
		ApiPort:           getEnvAsInt("API_PORT", 9090), // 新增: 默认 9090 作为 Mihomo 控制端 API 端口
		InternalBaseURL:   getEnv("INTERNAL_BASE_URL", "http://prefetch-proxy:8080"),
		Debug:             getEnv("DEBUG", "false") == "true",
		RuleListPath:      getEnv("RULE_LIST_PATH", ""),
		FakeIPFilterPath:  getEnv("FAKE_IP_FILTER_PATH", ""),
		PrivateConfigPath: getEnv("PRIVATE_CONFIG_PATH", ""),
	}
	domains := getEnv("TARGET_DOMAINS", "")
	if domains != "" {
		cfg.TargetDomains = strings.Split(domains, ",")
	}
	if cfg.PrivateConfigPath != "" {
		privateNodes, err := privateconfig.Load(cfg.PrivateConfigPath)
		if err != nil {
			return nil, nil, err
		}
		return cfg, privateNodes, nil
	}
	return cfg, nil, nil
}
