package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"
)

func controlSocketPath() string {
	return getEnv("CONTROL_SOCKET_PATH", "/tmp/prefetch-proxy.sock")
}

// Check validates the startup configuration and the files used by reload.
func Check() error {
	cfg, nodes, err := loadConfig()
	if err != nil {
		return fmt.Errorf("配置检查失败: %w", err)
	}
	if _, err := NewService(cfg, nodes).Handler(); err != nil {
		return fmt.Errorf("配置检查失败: %w", err)
	}
	fmt.Println("配置检查通过")
	return nil
}

// Reload asks the running process to validate and atomically apply its files.
func Reload() error {
	path := controlSocketPath()
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", path)
		}},
	}
	resp, err := client.Post("http://unix/reload", "", nil)
	if err != nil {
		return fmt.Errorf("无法连接运行中的 Prefetch Proxy（%s）: %w", path, err)
	}
	defer resp.Body.Close()
	message, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return fmt.Errorf("读取 reload 结果失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("reload 被拒绝: %s", strings.TrimSpace(string(message)))
	}
	fmt.Println("配置重新加载成功")
	return nil
}

func serveControl(service *Service) (func(), error) {
	path := controlSocketPath()
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("控制套接字路径已被非套接字文件占用: %s", path)
		}
		conn, err := net.DialTimeout("unix", path, time.Second)
		if err == nil {
			conn.Close()
			return nil, fmt.Errorf("控制套接字已由其他进程使用: %s", path)
		}
		if !errors.Is(err, syscall.ECONNREFUSED) {
			return nil, fmt.Errorf("检查已有控制套接字: %w", err)
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("清理旧控制套接字: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("检查控制套接字: %w", err)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("创建控制套接字: %w", err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		listener.Close()
		os.Remove(path)
		return nil, fmt.Errorf("设置控制套接字权限: %w", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /reload", func(w http.ResponseWriter, r *http.Request) {
		if err := service.reloadFileConfig(); err != nil {
			log.Printf("配置重新加载失败，继续使用旧配置: %v", err)
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		log.Printf("私有节点与固定订阅配置已重新加载")
		w.WriteHeader(http.StatusOK)
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("控制套接字服务失败: %v", err)
		}
	}()
	return func() {
		server.Close()
		os.Remove(path)
	}, nil
}
