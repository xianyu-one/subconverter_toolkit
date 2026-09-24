package app

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strings"
)

// --- 辅助函数 ---

// isTargetDomain 校验给定的订阅链接是否匹配我们配置的目标域名
func (s *Service) isTargetDomain(u string) bool {
	parsedURL, err := url.Parse(u)
	host := ""
	if err == nil {
		host = parsedURL.Hostname()
	}

	// 此处检查域名前不需要执行脱敏逻辑，以确保精准匹配
	for _, domain := range s.cfg.TargetDomains {
		// 1. 标准 Hostname 匹配 (例如 https://api.example.com/sub)
		if host != "" && strings.Contains(host, domain) {
			s.debugLog("isTargetDomain 命中 [Hostname匹配]: %s 包含 %s", host, domain)
			return true
		}
		// 2. 兜底匹配：当链接缺少协议(http/https)导致 Hostname 解析为空时，直接在原始字符串中匹配
		if host == "" && strings.Contains(u, domain) {
			s.debugLog("isTargetDomain 命中 [字符串兜底匹配]: %s 包含 %s", maskLogURL(u), domain)
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
