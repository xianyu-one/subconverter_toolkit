package rules

import (
	"gopkg.in/yaml.v3"
	"log"
	"net"
	"os"
	"strings"
	"sync"
)

// updateRuleFiles 解析获得的节点 YAML 数据，提取真实域名并同步更新到指定文件
type Updater struct {
	RuleListPath     string
	FakeIPFilterPath string
	Debug            bool
	mu               sync.Mutex
}

func (u *Updater) Update(realData []byte) {
	if u.RuleListPath == "" && u.FakeIPFilterPath == "" {
		return // 未配置文件路径，不执行操作
	}

	var config struct {
		Proxies []map[string]interface{} `yaml:"proxies"`
	}
	err := yaml.Unmarshal(realData, &config)
	if err != nil {
		log.Printf("更新规则文件失败，解析 YAML 错误: %v", err)
		return
	}

	var domains []string
	domainMap := make(map[string]bool)

	// 遍历所有节点，提取 "server" 字段
	for _, proxy := range config.Proxies {
		if server, ok := proxy["server"].(string); ok {
			// 过滤出真正的域名，排除掉 IPv4 / IPv6
			if isDomain(server) && !domainMap[server] {
				domains = append(domains, server)
				domainMap[server] = true
			}
		}
	}

	if len(domains) == 0 {
		if u.Debug {
			log.Printf("[DEBUG] 未提取到任何节点域名，跳过更新规则文件")
		}
		return
	}

	// 使用全局互斥锁保证文件写入的原子性和安全性，避免写坏文件
	u.mu.Lock()
	defer u.mu.Unlock()

	// 写入 Rule List (追加模式)
	if u.RuleListPath != "" {
		appendToFileUnique(u.RuleListPath, domains, func(content string) string {
			return "DOMAIN-SUFFIX,"
		})
	}

	// 写入 Fake-IP Filter 模板 (带智能缩进检测，适配用户的原有排版)
	if u.FakeIPFilterPath != "" {
		appendToFileUnique(u.FakeIPFilterPath, domains, func(content string) string {
			// 扫描文件找出用户自用的数组缩进习惯
			lines := strings.Split(content, "\n")
			for _, line := range lines {
				// 寻找类似于 "- " 或 "  - " 的列表结构
				idx := strings.Index(line, "- ")
				if idx != -1 {
					// 确保前置的字符全都是空格或是空字符串
					if strings.TrimSpace(line[:idx]) == "" {
						// 按照用户的缩进加上 "+." 构建格式
						return line[:idx] + "- +."
					}
				}
			}
			// 如果是空文件或者没有找到标准列表，则默认无前置空格格式
			return "- +."
		})
	}
}

// isDomain 检查传入的地址是否为域名 (粗略判断：无法被解析为合法 IP 即认为是域名)
func isDomain(address string) bool {
	if address == "" {
		return false
	}
	return net.ParseIP(address) == nil
}

// appendToFileUnique 检查行是否存在，不存在则追加到文件中，保证文件内容唯一不重复
func appendToFileUnique(filePath string, domains []string, detectPrefixFunc func(string) string) {
	content, err := os.ReadFile(filePath)
	if err != nil && !os.IsNotExist(err) {
		log.Printf("读取文件失败 %s: %v", filePath, err)
		return
	}

	// 动态检测文件中对应的格式前缀（缩进等）
	prefix := detectPrefixFunc(string(content))

	// 用 HashSet 记录当前文件中已存在的行，防止重复添加
	existingMap := make(map[string]bool)
	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			existingMap[trimmed] = true
		}
	}

	var linesToAdd []string
	for _, domain := range domains {
		lineStr := prefix + domain
		trimmed := strings.TrimSpace(lineStr) // 去除空白用于比对唯一性
		if !existingMap[trimmed] {
			linesToAdd = append(linesToAdd, lineStr) // 追加时使用带有计算好的格式/缩进原文
			existingMap[trimmed] = true              // 防止单次处理产生的多个重复记录
		}
	}

	if len(linesToAdd) == 0 {
		return // 所有域名都已存在，无需更新
	}

	// 打开文件追加模式
	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("打开文件失败 %s: %v", filePath, err)
		return
	}
	defer f.Close()

	// 若文件非空，且结尾没有换行符，则先补齐换行符保证格式不被破坏
	if len(content) > 0 && content[len(content)-1] != '\n' {
		f.WriteString("\n")
	}

	// 写入新行
	for _, line := range linesToAdd {
		f.WriteString(line + "\n")
	}
	log.Printf("已向文件 %s 追加了 %d 条新规则", filePath, len(linesToAdd))
}
