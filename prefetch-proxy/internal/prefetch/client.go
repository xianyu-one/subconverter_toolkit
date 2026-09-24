package prefetch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"gopkg.in/yaml.v3"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"time"
)

type Node map[string]interface{}

type Config struct {
	MihomoPath string
	ProxyPort  int
	ApiPort    int
	Debug      bool
}

type Client struct {
	cfg      Config
	debugLog func(string, ...interface{})
	maskURL  func(string) string
}

func NewClient(cfg Config, debugLog func(string, ...interface{}), maskURL func(string) string) *Client {
	return &Client{cfg: cfg, debugLog: debugLog, maskURL: maskURL}
}

// fetchPreNodes 获取并提取所有前置节点 (未经过代理)
func (c *Client) FetchPreNodes(targetURL string) ([]Node, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	c.debugLog("fetchPreNodes: 开始请求 %s", c.maskURL(targetURL))
	req, _ := http.NewRequestWithContext(ctx, "GET", targetURL, nil)
	req.Header.Set("User-Agent", "ClashforWindows/0.19.23") // 伪装 UA 防止被简单反扒拦截

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("获取前置订阅失败: %w", err)
	}
	defer resp.Body.Close()

	c.debugLog("fetchPreNodes: HTTP 状态码: %d", resp.StatusCode)

	body, _ := io.ReadAll(resp.Body)
	c.debugLog("fetchPreNodes: 获取到响应体，长度: %d 字节", len(body))

	var config struct {
		Proxies []Node `yaml:"proxies"`
	}
	err = yaml.Unmarshal(body, &config)
	if err != nil {
		return nil, fmt.Errorf("解析 YAML 失败: %w", err)
	}

	if len(config.Proxies) == 0 {
		return nil, fmt.Errorf("未找到任何前置节点")
	}

	c.debugLog("fetchPreNodes: 成功解析出 %d 个前置节点", len(config.Proxies))
	return config.Proxies, nil
}

// startTempProxy 生成包含 external-controller 控制端的 Mihomo 配置文件，并作为子进程启动
func (c *Client) StartTempProxy(preNodes []Node) (*exec.Cmd, string, []string, error) {
	cfg := c.cfg
	var nodeNames []string
	for _, n := range preNodes {
		if name, ok := n["name"].(string); ok {
			nodeNames = append(nodeNames, name)
		}
	}

	// 构造临时 Mihomo 代理配置
	// 使用 rule 模式配合 select 策略组，由外部代码通过 API 指挥切换节点
	tempConfig := map[string]interface{}{
		"socks-port":          cfg.ProxyPort,
		"allow-lan":           false,
		"mode":                "rule",
		"log-level":           "warning",
		"external-controller": fmt.Sprintf("127.0.0.1:%d", cfg.ApiPort), // 新增：暴露 API 控制端
		"proxies":             preNodes,
		"proxy-groups": []map[string]interface{}{
			{
				"name":    "Select-Proxy",
				"type":    "select", // 更改为 select 类型，准备接受 API 控制
				"proxies": nodeNames,
			},
		},
		"rules": []string{
			"MATCH,Select-Proxy", // 将所有流量强制路由到 Select 组
		},
	}

	configBytes, err := yaml.Marshal(tempConfig)
	if err != nil {
		return nil, "", nil, err
	}

	if cfg.Debug {
		c.debugLog("startTempProxy: 临时 Mihomo 配置已生成，启用了 API 控制端 (端口: %d)，包含 %d 个节点", cfg.ApiPort, len(nodeNames))
	}

	// 建立系统临时文件写入配置文件
	tempFile, err := os.CreateTemp("", "mihomo_temp_*.yaml")
	if err != nil {
		return nil, "", nil, err
	}
	tempFilePath := tempFile.Name()

	if _, err := tempFile.Write(configBytes); err != nil {
		tempFile.Close()
		os.Remove(tempFilePath)
		return nil, "", nil, err
	}
	tempFile.Close()

	// 启动 Mihomo 子进程
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
		return nil, "", nil, fmt.Errorf("启动 Mihomo 失败: %w", err)
	}

	// 给予内核启动和建立 API 服务缓冲时间
	log.Printf("Mihomo 引擎和 API 接口正在启动...")
	time.Sleep(2 * time.Second)

	// 返回提取出的 nodeNames 列表，方便在重试时利用 API 指定节点
	return cmd, tempFilePath, nodeNames, nil
}

// selectMihomoNode 通过 Mihomo 的 REST API 手动切换指定策略组到给定的目标节点
func selectMihomoNode(apiPort int, groupName, nodeName string) error {
	apiUrl := fmt.Sprintf("http://127.0.0.1:%d/proxies/%s", apiPort, url.PathEscape(groupName))

	// 构造切换节点的 JSON 请求体
	payloadData := map[string]string{"name": nodeName}
	jsonData, err := json.Marshal(payloadData)
	if err != nil {
		return fmt.Errorf("JSON 序列化失败: %w", err)
	}

	req, err := http.NewRequest("PUT", apiUrl, bytes.NewReader(jsonData))
	if err != nil {
		return fmt.Errorf("创建 API 请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("API 请求执行失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("返回异常状态码 %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// fetchRealNodesWithRetry 通过本地 Mihomo 代理获取真实节点数据
// 新增逻辑：遍历 nodeNames 列表，使用 API 显式要求 Mihomo 切换前置节点进行重试
func (c *Client) FetchRealNodesWithRetry(targetURL string, proxyPort int, apiPort int, nodeNames []string, maxRetries int) ([]byte, error) {
	proxyStr := fmt.Sprintf("socks5://127.0.0.1:%d", proxyPort)
	proxyURL, _ := url.Parse(proxyStr)

	transport := &http.Transport{
		Proxy: http.ProxyURL(proxyURL),
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   15 * time.Second, // 设置单次 HTTP 请求合理的超时时间
	}

	var lastErr error

	// 如果前置节点总数小于设定的重试次数，则最多尝试所有节点一遍即可
	limit := maxRetries
	if len(nodeNames) < limit {
		limit = len(nodeNames)
	}

	// 循环执行请求重试，并主动分配节点
	for attempt := 0; attempt < limit; attempt++ {
		// 挑选本轮重试的前置节点
		currentNode := nodeNames[attempt]
		log.Printf("fetchRealNodes: [尝试 %d/%d] 正在通过 API 选择前置节点: %s", attempt+1, limit, currentNode)

		// 调用 API 切换 Mihomo 策略
		err := selectMihomoNode(apiPort, "Select-Proxy", currentNode)
		if err != nil {
			lastErr = fmt.Errorf("API 切换节点 [%s] 失败: %w", currentNode, err)
			log.Printf("fetchRealNodes: %v", lastErr)
			continue
		}

		// 给予 Mihomo 缓冲时间应用新的节点链路
		time.Sleep(500 * time.Millisecond)

		c.debugLog("fetchRealNodes: 发起 SOCKS5 代理请求: %s", c.maskURL(targetURL))

		req, _ := http.NewRequest("GET", targetURL, nil)
		req.Header.Set("User-Agent", "ClashforWindows/0.19.23")

		resp, err := client.Do(req)

		// 如果请求报错 (通常是代理节点离线、连接拒接或超时)
		if err != nil {
			lastErr = fmt.Errorf("节点 [%s] 请求失败: %w", currentNode, err)
			log.Printf("fetchRealNodes: 前置节点 [%s] 失败，准备尝试下一个", currentNode)
			continue
		}

		// 如果状态码不是 200 OK
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastErr = fmt.Errorf("节点 [%s] 请求返回非 200 状态码: %d", currentNode, resp.StatusCode)
			log.Printf("fetchRealNodes: 前置节点 [%s] 获取失败(HTTP %d)，准备尝试下一个", currentNode, resp.StatusCode)
			continue
		}

		// 成功返回 200 OK，读取并返回内容
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()

		if readErr == nil {
			log.Printf("fetchRealNodes: 前置节点 [%s] 成功获取真实订阅数据！长度: %d 字节", currentNode, len(body))
			return body, nil
		} else {
			lastErr = fmt.Errorf("读取节点 [%s] 响应体失败: %w", currentNode, readErr)
		}
	}

	return nil, fmt.Errorf("获取真实节点失败，已尝试完设定的节点数 (%d)，最后一次错误: %v", limit, lastErr)
}
