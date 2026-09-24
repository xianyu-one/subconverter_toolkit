package app

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// processRegularSubscriptions 处理不需要二次代理的常规订阅
// 该函数利用后端的 Subconverter 预先将其转换为 Clash 格式，以便提取节点域名并写入规则文件
func (s *Service) processRegularSubscriptions(joinedSubs string) {
	if s.cfg.RuleListPath == "" && s.cfg.FakeIPFilterPath == "" {
		// 如果用户没配置需要写入规则文件，就没必要进行预解析了
		return
	}

	hash := md5Hash("regular_" + joinedSubs)
	// 如果缓存中已经存在，说明最近解析过，避免频繁请求后端
	if s.cache.Get(hash) != nil {
		s.debugLog("常规订阅组合已预解析过，跳过域名提取")
		return
	}

	var mtx sync.Mutex
	v, _ := s.lockMap.LoadOrStore(hash, &mtx)
	mu := v.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	// 获得锁后二次检查缓存 (Double-check locking)
	if s.cache.Get(hash) != nil {
		return
	}

	log.Printf("开始预解析常规订阅以提取域名...")

	// 构造向后端的 Subconverter 请求，强制 target=clash 以便解析 yaml
	baseURL := strings.TrimRight(s.cfg.SubconverterURL, "/")
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
			s.rules.Update(body)
			// 设置 1 小时缓存标记，避免短期内对完全相同的常规订阅组合重复请求解析
			s.cache.Set(hash, []byte("processed"), 1*time.Hour)
			log.Printf("常规订阅域名提取完成")
		}
	} else {
		log.Printf("解析常规订阅时 Subconverter 返回非 200 状态码: %d", resp.StatusCode)
	}
}
