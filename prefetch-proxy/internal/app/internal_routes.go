package app

import (
	"net/http"
	"strings"
)

// handlePrivateNodes 响应 Subconverter 获取私有节点配置的请求
// 将保存在本地磁盘上的私有节点注入到订阅中
func (s *Service) handlePrivateNodes(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/internal/private/")
	if r.Method != http.MethodGet || strings.Contains(id, "/") || id == "" {
		http.NotFound(w, r)
		return
	}
	data, ok := s.privateLinks.Get(id)
	if !ok {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(data)
}

// handleInternalSub 响应 Subconverter 请求本服务获取已预提取和缓存好的订阅配置
func (s *Service) handleInternalSub(w http.ResponseWriter, r *http.Request) {
	hash := strings.TrimPrefix(r.URL.Path, "/internal/")
	if hash == "" {
		http.Error(w, "无效的请求", http.StatusBadRequest)
		return
	}

	s.debugLog("Subconverter 正在拉取内部缓存，哈希: %s", hash)

	// 从缓存读取已获取的节点数据
	data := s.cache.Get(hash)
	if data == nil {
		s.debugLog("请求的缓存未找到或已过期: %s", hash)
		http.Error(w, "订阅缓存已过期或不存在", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(data)
}
