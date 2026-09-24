package app

import (
	"fmt"
	"log"
	"os"
	"sync"
	"time"
)

// processSubscription 执行二次代理（预获取）的核心逻辑
// 1. 直连获取前置节点 -> 2. 本地拉起 Mihomo -> 3. 通过 Mihomo 走前置节点获取真实订阅 -> 4. 提取域名并缓存结果
func (s *Service) processSubscription(targetURL string) (string, error) {
	hash := md5Hash(targetURL)
	s.debugLog("处理订阅 URL 哈希值: %s", hash)

	// 1. 检查缓存是否命中
	if s.cache.Get(hash) != nil {
		log.Printf("命中缓存: %s", maskLogURL(targetURL))
		return fmt.Sprintf("%s/internal/%s", s.cfg.InternalBaseURL, hash), nil
	}

	// 使用 Mutex 防止对同一 URL 并发启动多个 Mihomo 进程
	var mtx sync.Mutex
	v, _ := s.lockMap.LoadOrStore(hash, &mtx)
	mu := v.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	// 获得锁后二次检查缓存 (Double-check locking)
	if s.cache.Get(hash) != nil {
		return fmt.Sprintf("%s/internal/%s", s.cfg.InternalBaseURL, hash), nil
	}

	log.Printf("开始预获取前置节点: %s", maskLogURL(targetURL))

	// 2. 获取前置节点（未翻墙环境获取的原始订阅节点）
	preNodes, err := s.prefetch.FetchPreNodes(targetURL)
	if err != nil {
		return "", err
	}

	// 3. 启动临时 Mihomo 代理（提取节点名称并采用 Select 手动策略组）
	cmd, tempFilePath, nodeNames, err := s.prefetch.StartTempProxy(preNodes)
	if err != nil {
		return "", err
	}
	// 确保不论发生什么错误，都会彻底清理衍生的 Mihomo 进程和临时配置文件
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

	// 4. 获取真实节点 (配合显式 API 节点切换重试机制)
	log.Printf("通过本地 SOCKS5 获取真实节点...")
	// 设定重试次数为3，传入解析到的前置节点名称列表
	realData, err := s.prefetch.FetchRealNodesWithRetry(targetURL, s.cfg.ProxyPort, s.cfg.ApiPort, nodeNames, 3)
	if err != nil {
		return "", err
	}

	// 5. 等待真实节点中的域名提取并写入本地规则文件
	// 重要：这里必须是同步阻塞调用，确保提取完成后再将订阅移交给 Subconverter
	s.rules.Update(realData)

	// 6. 将成功获取的真实节点数据写入缓存 (TTL: 1小时)
	s.debugLog("将真实节点数据写入缓存，设置过期时间为 1 小时")
	s.cache.Set(hash, realData, 1*time.Hour)

	// 返回内部短链，Subconverter 接下来将访问此链接获取缓存数据
	return fmt.Sprintf("%s/internal/%s", s.cfg.InternalBaseURL, hash), nil
}
