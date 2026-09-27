package app

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const maxSubscriptionSize = 16 << 20

var subscriptionHTTPClient = &http.Client{Timeout: 30 * time.Second}

func subscriptionCacheKey(source, userAgent string, target bool) string {
	kind := "regular"
	if target {
		kind = "target"
	}
	return md5Hash("subscription\x00" + kind + "\x00" + userAgent + "\x00" + source)
}

func (s *Service) internalSubscriptionURL(key string) string {
	return strings.TrimRight(s.cfg.InternalBaseURL, "/") + "/internal/" + key
}

// processSubscription fetches every HTTP subscription in this service. Target
// domains retain the two-stage Mihomo flow; other domains use a direct GET.
// A failed refresh leaves the previous cache entry untouched.
func (s *Service) processSubscription(r *http.Request, source, userAgent string, noCache, target bool) (string, error) {
	key := subscriptionCacheKey(source, userAgent, target)
	link := s.internalSubscriptionURL(key)
	var mtx sync.Mutex
	v, _ := s.lockMap.LoadOrStore(key, &mtx)
	mu := v.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	if !noCache && s.cache.Get(key) != nil {
		if s.hasUpstreamNodes(r, link) {
			s.debugLog("命中订阅缓存: %s", maskLogURL(source))
			return link, nil
		}
		s.cache.Delete(key)
		return "", fmt.Errorf("cached subscription has no parseable nodes")
	}

	var data []byte
	var err error
	if target {
		data, err = s.fetchTargetSubscription(source, userAgent)
	} else {
		data, err = fetchRegularSubscription(r, source, userAgent)
	}
	if err != nil {
		return "", err
	}

	// Subconverter must parse the new bytes before they replace a working entry.
	id, err := s.issueInternal(data, "validation:", time.Minute, nil)
	if err != nil {
		return "", err
	}
	validationKey := "validation:" + id
	valid := s.hasUpstreamNodes(r, s.internalSubscriptionURL(validationKey))
	s.cache.Delete(validationKey)
	if !valid {
		return "", fmt.Errorf("subscription has no parseable nodes")
	}

	if target {
		s.rules.Update(data)
	}
	s.cache.Set(key, data, s.cfg.CacheTTL)
	return link, nil
}

func fetchRegularSubscription(r *http.Request, source, userAgent string) ([]byte, error) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, source, nil)
	if err != nil {
		return nil, err
	}
	req.URL.Scheme = strings.ToLower(req.URL.Scheme)
	req.Header.Set("User-Agent", userAgent)
	resp, err := subscriptionHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream returned HTTP %d", resp.StatusCode)
	}
	return readLimited(resp.Body, maxSubscriptionSize)
}

// fetchTargetSubscription keeps the existing two-stage proxy process.
func (s *Service) fetchTargetSubscription(source, userAgent string) ([]byte, error) {
	log.Printf("开始预获取前置节点: %s", maskLogURL(source))
	preNodes, err := s.prefetch.FetchPreNodes(source, userAgent)
	if err != nil {
		return nil, err
	}
	cmd, tempFilePath, nodeNames, err := s.prefetch.StartTempProxy(preNodes)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cmd != nil && cmd.Process != nil {
			s.debugLog("关闭临时 Mihomo 进程 PID: %d", cmd.Process.Pid)
			cmd.Process.Kill()
			cmd.Wait()
		}
		if tempFilePath != "" {
			os.Remove(tempFilePath)
		}
	}()
	log.Printf("通过本地 SOCKS5 获取真实节点...")
	return s.prefetch.FetchRealNodesWithRetry(source, userAgent, s.cfg.ProxyPort, s.cfg.ApiPort, nodeNames, 3)
}
