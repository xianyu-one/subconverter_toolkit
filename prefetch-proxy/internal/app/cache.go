package app

import (
	"sync"
	"time"
)

type CacheItem struct {
	Data      []byte
	ExpiresAt time.Time
}

type CacheManager struct {
	mu    sync.RWMutex
	items map[string]CacheItem
}

// --- 缓存管理器实现 ---

// Set 写入缓存
func (c *CacheManager) Set(key string, data []byte, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[key] = CacheItem{
		Data:      data,
		ExpiresAt: time.Now().Add(ttl),
	}
}

// Get 读取缓存，若缓存已过期则自动失效并返回 nil
func (c *CacheManager) Get(key string) []byte {
	c.mu.RLock()
	defer c.mu.RUnlock()
	item, found := c.items[key]
	if !found {
		return nil
	}
	// 如果当前时间已经超过了设定的过期时间
	if time.Now().After(item.ExpiresAt) {
		return nil
	}
	return item.Data
}

func (c *CacheManager) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, key)
}
