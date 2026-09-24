package privateconfig

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

type Node map[string]interface{}

type keySpec struct {
	Name   string   `yaml:"name"`
	Token  string   `yaml:"token"`
	Nodes  []string `yaml:"nodes"`
	Groups []string `yaml:"groups"`
}

type fileSpec struct {
	Proxies []Node    `yaml:"proxies"`
	Keys    []keySpec `yaml:"keys"`
}

// Config is an immutable, validated mapping from bearer tokens to Clash nodes.
type Config struct {
	byToken map[string][]Node
	names   map[string]string
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read private config: %w", err)
	}
	return Parse(data)
}

func Parse(data []byte) (*Config, error) {
	var spec fileSpec
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&spec); err != nil {
		return nil, formatYAMLError(err)
	}
	var extra interface{}
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("private config contains multiple YAML documents")
		}
		return nil, formatYAMLError(err)
	}
	if len(spec.Keys) == 0 {
		return nil, fmt.Errorf("private config requires keys")
	}

	names := make(map[string]bool)
	groups := make(map[string]bool)
	nodeGroups := make([]map[string]bool, len(spec.Proxies))
	for i, node := range spec.Proxies {
		name, ok := node["name"].(string)
		if !ok || strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("proxies[%d].name is required", i)
		}
		if names[name] {
			return nil, fmt.Errorf("duplicate proxy name %q", name)
		}
		names[name] = true
		if kind, ok := node["type"].(string); !ok || strings.TrimSpace(kind) == "" {
			return nil, fmt.Errorf("proxies[%d].type is required", i)
		}
		if server, ok := node["server"].(string); !ok || strings.TrimSpace(server) == "" {
			return nil, fmt.Errorf("proxies[%d].server is required", i)
		}
		nodeGroups[i] = make(map[string]bool)
		if raw, exists := node["groups"]; exists {
			values, ok := raw.([]interface{})
			if !ok {
				return nil, fmt.Errorf("proxies[%d].groups must be a list", i)
			}
			for _, value := range values {
				group, ok := value.(string)
				if !ok || strings.TrimSpace(group) == "" {
					return nil, fmt.Errorf("proxies[%d].groups contains an empty or invalid name", i)
				}
				groups[group] = true
				nodeGroups[i][group] = true
			}
		}
	}

	result := &Config{byToken: make(map[string][]Node, len(spec.Keys)), names: make(map[string]string, len(spec.Keys))}
	keyNames := make(map[string]bool)
	for i, key := range spec.Keys {
		if strings.TrimSpace(key.Name) == "" || strings.TrimSpace(key.Token) == "" {
			return nil, fmt.Errorf("keys[%d] requires name and token", i)
		}
		if keyNames[key.Name] {
			return nil, fmt.Errorf("duplicate key name %q", key.Name)
		}
		keyNames[key.Name] = true
		if _, exists := result.byToken[key.Token]; exists {
			return nil, fmt.Errorf("keys[%d] duplicates another token", i)
		}
		result.byToken[key.Token] = nil
		result.names[key.Token] = key.Name
		wantedNames := make(map[string]bool)
		wantedGroups := make(map[string]bool)
		for _, name := range key.Nodes {
			if !names[name] {
				return nil, fmt.Errorf("keys[%d] references unknown node %q", i, name)
			}
			wantedNames[name] = true
		}
		for _, group := range key.Groups {
			if !groups[group] {
				return nil, fmt.Errorf("keys[%d] references unknown group %q", i, group)
			}
			wantedGroups[group] = true
		}
		for j, node := range spec.Proxies {
			name := node["name"].(string)
			selected := wantedNames[name]
			for group := range wantedGroups {
				selected = selected || nodeGroups[j][group]
			}
			if selected {
				clean := make(Node, len(node))
				for field, value := range node {
					if field != "groups" {
						clean[field] = value
					}
				}
				if !strings.HasPrefix(name, "🔒私有") {
					clean["name"] = "🔒私有 - " + name
				}
				clean["dialer-proxy"] = "🚀 前置节点池"
				result.byToken[key.Token] = append(result.byToken[key.Token], clean)
			}
		}
	}
	return result, nil
}

func (c *Config) Name(token string) (string, bool) {
	if c == nil {
		return "", false
	}
	name, ok := c.names[token]
	return name, ok
}

func (c *Config) HasName(name string) bool {
	if c == nil {
		return false
	}
	for _, candidate := range c.names {
		if candidate == name {
			return true
		}
	}
	return false
}

func (c *Config) Select(token string) ([]Node, bool) {
	if c == nil {
		return nil, false
	}
	nodes, ok := c.byToken[token]
	if !ok {
		return nil, false
	}
	selected := make([]Node, len(nodes))
	for i, node := range nodes {
		selected[i] = cloneValue(node).(Node)
	}
	return selected, true
}

var yamlLine = regexp.MustCompile(`line [0-9]+`)

func formatYAMLError(err error) error {
	reason := "invalid YAML syntax"
	message := err.Error()
	switch {
	case strings.Contains(message, "not found in type"):
		reason = "unknown field"
	case strings.Contains(message, "cannot unmarshal"):
		reason = "invalid value type"
	case strings.Contains(message, "already defined"):
		reason = "duplicate YAML field"
	}
	if line := yamlLine.FindString(message); line != "" {
		return fmt.Errorf("private config %s at %s", reason, line)
	}
	return fmt.Errorf("private config %s", reason)
}

func cloneValue(value interface{}) interface{} {
	switch item := value.(type) {
	case Node:
		copy := make(Node, len(item))
		for key, nested := range item {
			copy[key] = cloneValue(nested)
		}
		return copy
	case map[string]interface{}:
		copy := make(map[string]interface{}, len(item))
		for key, nested := range item {
			copy[key] = cloneValue(nested)
		}
		return copy
	case []interface{}:
		copy := make([]interface{}, len(item))
		for i, nested := range item {
			copy[i] = cloneValue(nested)
		}
		return copy
	default:
		return item
	}
}
