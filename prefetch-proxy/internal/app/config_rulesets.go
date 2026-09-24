package app

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const (
	configLinkTTL  = 10 * time.Minute
	rulesetLinkTTL = 21 * time.Minute
	maxConfigSize  = 2 << 20
	maxRulesetSize = 16 << 20
)

var configHTTPClient = &http.Client{Timeout: 30 * time.Second}

type rulesetLink struct {
	id         string
	reuseUntil time.Time
}

func httpURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (strings.EqualFold(u.Scheme, "http") || strings.EqualFold(u.Scheme, "https")) && u.Hostname() != ""
}

func (s *Service) issueInternal(data []byte, prefix string, ttl time.Duration, onExpiry func(string)) (string, error) {
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	id := base64.RawURLEncoding.EncodeToString(random[:])
	key := prefix + id
	s.cache.Set(key, data, ttl)
	time.AfterFunc(ttl, func() {
		s.cache.Delete(key)
		if onExpiry != nil {
			onExpiry(id)
		}
	})
	return id, nil
}

func (s *Service) issueRuleset(source string) (string, error) {
	s.ruleLinksMu.Lock()
	defer s.ruleLinksMu.Unlock()
	if link := s.ruleLinkIDs[source]; time.Now().Before(link.reuseUntil) && s.cache.Get("ruleset:"+link.id) != nil {
		return link.id, nil
	}
	id, err := s.issueInternal([]byte(source), "ruleset:", rulesetLinkTTL, func(expiredID string) {
		s.ruleLinksMu.Lock()
		if s.ruleLinkIDs[source].id == expiredID {
			delete(s.ruleLinkIDs, source)
		}
		s.ruleLinksMu.Unlock()
	})
	if err != nil {
		return "", err
	}
	// A newly issued config lives for 10 minutes, even if this link is reused.
	s.ruleLinkIDs[source] = rulesetLink{id: id, reuseUntil: time.Now().Add(rulesetLinkTTL - configLinkTTL - time.Minute)}
	return id, nil
}

func (s *Service) readConfig(r *http.Request, source string) ([]byte, error) {
	if httpURL(source) {
		return fetchText(r, source, maxConfigSize)
	}
	if s.cfg.ConfigDir == "" || strings.Contains(source, ":") || filepath.IsAbs(source) {
		return nil, fmt.Errorf("config source is not accessible to prefetch-proxy")
	}
	clean := filepath.Clean(source)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("config path is outside CONFIG_DIR")
	}
	// The configured directory is the same root used for relative Subconverter config paths.
	filename := filepath.Join(s.cfg.ConfigDir, clean)
	root, err := filepath.EvalSymlinks(s.cfg.ConfigDir)
	if err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(filename)
	if err != nil {
		return nil, err
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("config path is outside CONFIG_DIR")
	}
	file, err := os.Open(resolved)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return readLimited(file, maxConfigSize)
}

func fetchText(r *http.Request, source string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, source, nil)
	if err != nil {
		return nil, err
	}
	req.URL.Scheme = strings.ToLower(req.URL.Scheme)
	resp, err := configHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("source returned HTTP %d", resp.StatusCode)
	}
	return readLimited(resp.Body, limit)
}

func readLimited(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("source exceeds %d bytes", limit)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("source is empty")
	}
	return data, nil
}

func (s *Service) prepareConfig(r *http.Request, source string) (string, error) {
	data, err := s.readConfig(r, source)
	if err != nil {
		return "", err
	}
	lines := strings.SplitAfter(string(data), "\n")
	for i, line := range lines {
		equal := strings.IndexByte(line, '=')
		if equal < 0 || !strings.EqualFold(strings.TrimSpace(line[:equal]), "ruleset") {
			continue
		}
		comma := strings.IndexByte(line[equal+1:], ',')
		if comma < 0 {
			continue
		}
		start := equal + 1 + comma + 1
		end := start
		for end < len(line) && line[end] != ',' && line[end] != '\r' && line[end] != '\n' {
			end++
		}
		field := line[start:end]
		sourceURL := strings.TrimSpace(field)
		if !httpURL(sourceURL) {
			continue
		}
		id, err := s.issueRuleset(sourceURL)
		if err != nil {
			return "", err
		}
		parsed, _ := url.Parse(sourceURL)
		name := path.Base(parsed.Path)
		if name == "." || name == "/" || name == "" {
			name = "rules.list"
		}
		internalURL := strings.TrimRight(s.cfg.InternalBaseURL, "/") + "/internal/ruleset/" + id + "/" + url.PathEscape(name)
		leftSpace := field[:len(field)-len(strings.TrimLeft(field, " \t"))]
		rightSpace := field[len(strings.TrimRight(field, " \t")):]
		lines[i] = line[:start] + leftSpace + internalURL + rightSpace + line[end:]
	}
	id, err := s.issueInternal([]byte(strings.Join(lines, "")), "config:", configLinkTTL, nil)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(s.cfg.InternalBaseURL, "/") + "/internal/config/" + id + "/config.ini", nil
}

func (s *Service) handleInternalConfig(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/internal/config/"), "/")
	if r.Method != http.MethodGet || len(parts) != 2 || parts[1] != "config.ini" {
		http.NotFound(w, r)
		return
	}
	data := s.cache.Get("config:" + parts[0])
	if data == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(data)
}

func (s *Service) handleInternalRuleset(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/internal/ruleset/"), "/")
	if r.Method != http.MethodGet || len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		http.NotFound(w, r)
		return
	}
	source := s.cache.Get("ruleset:" + parts[0])
	if source == nil {
		http.NotFound(w, r)
		return
	}
	data, err := fetchText(r, string(source), maxRulesetSize)
	if err != nil {
		s.debugLog("读取规则集失败: %s", maskLogURL(string(source)))
		http.Error(w, "Failed to fetch ruleset", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(data)
}
