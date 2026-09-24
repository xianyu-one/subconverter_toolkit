package app

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// hasUpstreamNodes checks upstreams without private nodes, so an injected node
// cannot hide a failed subscription. The backend parses the same URL items that
// it will later use for the final conversion.
func (s *Service) hasUpstreamNodes(original *http.Request, joined string) bool {
	endpoint := strings.TrimRight(s.cfg.SubconverterURL, "/") + "/sub"
	query := url.Values{"target": {"clash"}, "list": {"true"}, "url": {joined}}
	ctx, cancel := context.WithTimeout(original.Context(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+query.Encode(), nil)
	if err != nil {
		log.Printf("无法创建上游节点检查请求")
		return false
	}
	req.Header.Set("User-Agent", original.Header.Get("User-Agent"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("上游节点检查请求失败")
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return false
	}
	var result struct {
		Proxies []any `yaml:"proxies"`
		Legacy  []any `yaml:"Proxy"`
	}
	return yaml.Unmarshal(data, &result) == nil && len(result.Proxies)+len(result.Legacy) > 0
}
