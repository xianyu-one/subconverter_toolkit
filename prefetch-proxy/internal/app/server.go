package app

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

func Run() {
	cfg, privateNodes, err := loadConfig()
	if err != nil {
		log.Fatalf("配置加载失败: %v", err)
	}
	service := NewService(cfg, privateNodes)
	log.Printf("服务启动监听在 %s，Subconverter 后端: %s", cfg.ListenAddr, cfg.SubconverterURL)
	log.Printf("目标拦截域名: %v", cfg.TargetDomains)

	if cfg.RuleListPath != "" {
		log.Printf("已启用 Rule List 动态更新，路径: %s", cfg.RuleListPath)
	}
	if cfg.FakeIPFilterPath != "" {
		log.Printf("已启用 Fake-IP 模板动态更新，路径: %s", cfg.FakeIPFilterPath)
	}
	if privateNodes != nil {
		log.Printf("已加载密钥配置")
	}
	if cfg.CoverProfiles != nil {
		log.Printf("已启用固定订阅配置")
	}
	if cfg.Debug {
		log.Printf("调试模式 (DEBUG) 已开启")
	}

	handler, err := service.Handler()
	if err != nil {
		log.Fatalf("创建 HTTP 服务失败: %v", err)
	}
	server := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("服务运行失败: %v", err)
	}
}

func (s *Service) Handler() (http.Handler, error) {
	// 解析后端 Subconverter 的目标 URL
	targetURL, err := url.Parse(s.cfg.SubconverterURL)
	if err != nil {
		return nil, fmt.Errorf("解析 Subconverter URL 失败: %w", err)
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
	mux.HandleFunc("/internal/private", http.NotFound)
	mux.HandleFunc("/internal/private/", s.handlePrivateNodes)
	mux.HandleFunc("/internal/config/", s.handleInternalConfig)
	mux.HandleFunc("/internal/ruleset/", s.handleInternalRuleset)

	// 处理内部缓存请求短链，供 Subconverter 提取已经预获取好的节点数据
	mux.HandleFunc("/internal/", s.handleInternalSub)

	// 根路径拦截所有其他代理请求
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.handleProxyRequest(w, r, proxy)
	})

	return mux, nil
}
