package app

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"strings"

	"prefetch-proxy/internal/privateconfig"
)

// handleProxyRequest 处理并重写客户端发往 Subconverter 的请求
func (s *Service) handleProxyRequest(w http.ResponseWriter, r *http.Request, proxy *httputil.ReverseProxy) {
	query := r.URL.Query()
	hasChainToken := query.Has("chaintoken")
	var selected []privateconfig.Node
	var keyName string
	if hasChainToken {
		var ok bool
		selected, ok = s.privateNodes.Select(query.Get("chaintoken"))
		if !ok {
			http.Error(w, "Invalid chaintoken", http.StatusForbidden)
			return
		}
		keyName, _ = s.privateNodes.Name(query.Get("chaintoken"))
		query.Del("chaintoken")
		r.URL.RawQuery = query.Encode()
	}
	if r.URL.Path == "/sub" && query.Has("coverprofile") {
		if !hasChainToken {
			http.Error(w, "Invalid chaintoken", http.StatusForbidden)
			return
		}
		profile, ok := s.cfg.CoverProfiles.Select(keyName, query.Get("coverprofile"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		query.Del("coverprofile")
		if query.Has("url") && query.Get("url") == "" {
			http.Error(w, "Empty url", http.StatusBadRequest)
			return
		}
		if !query.Has("url") {
			query.Set("url", strings.Join(profile.Upstreams, "|"))
		}
		for name, value := range profile.Params {
			if !query.Has(name) {
				query.Set(name, value)
			}
		}
		r.URL.RawQuery = query.Encode()
	}
	if r.URL.Path == "/sub" && query.Get("config") != "" && (httpURL(query.Get("config")) || s.cfg.ConfigDir != "") {
		internalConfig, err := s.prepareConfig(r, query.Get("config"))
		if err != nil {
			log.Printf("处理配置文件失败 [%s]", maskLogURL(query.Get("config")))
			http.Error(w, "Failed to prepare config", http.StatusBadGateway)
			return
		}
		query.Set("config", internalConfig)
		r.URL.RawQuery = query.Encode()
	}
	urlParam := query.Get("url")

	s.debugLog("收到代理请求, Path: %s", r.URL.Path)

	if urlParam == "" {
		s.debugLog("请求未包含 url 参数，直接转发给后端")
		proxy.ServeHTTP(w, r)
		return
	}

	s.debugLog("解析到的 url 参数值: %s", maskLogURL(urlParam))

	// 拆分多个由 "|" 分隔的订阅链接
	requestedURLs := strings.Split(urlParam, "|")
	var subURLs []string
	var regularSubs []string
	modified := hasChainToken

	// 遍历所有订阅链接，区分是否需要经过二次代理预获取
	for _, subURL := range requestedURLs {
		if s.isTargetDomain(subURL) {
			s.debugLog("命中目标域名，开始预获取流程: %s", maskLogURL(subURL))
			internalLink, err := s.processSubscription(subURL)
			if err != nil {
				log.Printf("处理二次订阅失败 [%s]", maskLogURL(subURL))
				modified = true
				continue
			}
			if !s.hasUpstreamNodes(r, internalLink) {
				s.cache.Delete(md5Hash(subURL))
				log.Printf("二次订阅没有可解析节点 [%s]", maskLogURL(subURL))
				modified = true
				continue
			}
			s.debugLog("订阅转换为内部链接: %s -> %s", maskLogURL(subURL), internalLink)
			// 将原始订阅链接替换为本地缓存服务的短链
			subURLs = append(subURLs, internalLink)
			modified = true
		} else {
			s.debugLog("常规订阅记录（稍后将提取其域名）: %s", maskLogURL(subURL))
			regularSubs = append(regularSubs, subURL)
			subURLs = append(subURLs, subURL)
		}
	}
	if len(subURLs) == 0 {
		http.Error(w, "All upstreams failed", http.StatusBadGateway)
		return
	}
	if len(selected) > 0 && !s.hasUpstreamNodes(r, strings.Join(subURLs, "|")) {
		http.Error(w, "All upstreams failed", http.StatusBadGateway)
		return
	}

	// 链式代理：处理私有节点注入
	if len(selected) > 0 {
		id, err := s.privateLinks.Issue(selected)
		if err != nil {
			http.Error(w, "Failed to prepare private nodes", http.StatusInternalServerError)
			return
		}
		subURLs = append(subURLs, fmt.Sprintf("%s/internal/private/%s", strings.TrimRight(s.cfg.InternalBaseURL, "/"), id))
		modified = true
	}

	// 提取并写入常规订阅的域名（在后台解析写入）
	if len(regularSubs) > 0 {
		joinedRegularSubs := strings.Join(regularSubs, "|")
		s.processRegularSubscriptions(joinedRegularSubs)
	}

	// 如果参数发生了改变，重写 HTTP 请求参数
	if modified {
		query.Set("url", strings.Join(subURLs, "|"))
		r.URL.RawQuery = query.Encode()
		log.Printf("请求参数已修改发往 Subconverter (包含内部获取缓存链接或私有节点)")
	} else {
		s.debugLog("未修改任何订阅链接，原样转发")
	}

	// 将(可能修改过的)请求移交给内置的反向代理，发送给真实的 Subconverter
	proxy.ServeHTTP(w, r)
}
