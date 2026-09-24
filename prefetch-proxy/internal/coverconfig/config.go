package coverconfig

import (
	"fmt"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
	"prefetch-proxy/internal/privateconfig"
)

type Profile struct {
	Upstreams []string
	Params    map[string]string
}

type Config struct {
	profiles map[string]map[string]Profile
}

type fileSpec struct {
	Keys []keySpec `yaml:"keys"`
}

type keySpec struct {
	Name      string                 `yaml:"name"`
	Upstreams map[string]string      `yaml:"upstreams"`
	Profiles  map[string]profileSpec `yaml:"profiles"`
}

type profileSpec struct {
	Upstreams []string          `yaml:"upstreams"`
	Params    map[string]string `yaml:"params"`
}

func Load(path string, keys *privateconfig.Config) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read cover profile config: %w", err)
	}
	return Parse(data, keys)
}

func Parse(data []byte, keys *privateconfig.Config) (*Config, error) {
	var spec fileSpec
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&spec); err != nil {
		return nil, fmt.Errorf("invalid cover profile config: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("cover profile config must contain one YAML document")
	}
	if len(spec.Keys) == 0 {
		return nil, fmt.Errorf("cover profile config requires keys")
	}
	result := &Config{profiles: make(map[string]map[string]Profile, len(spec.Keys))}
	for _, key := range spec.Keys {
		if key.Name == "" || !keys.HasName(key.Name) {
			return nil, fmt.Errorf("cover profile references unknown key %q", key.Name)
		}
		if _, exists := result.profiles[key.Name]; exists {
			return nil, fmt.Errorf("duplicate cover profile key %q", key.Name)
		}
		if len(key.Profiles) == 0 {
			return nil, fmt.Errorf("key %q requires profiles", key.Name)
		}
		profiles := make(map[string]Profile, len(key.Profiles))
		for name, profile := range key.Profiles {
			if strings.TrimSpace(name) == "" || len(profile.Upstreams) == 0 {
				return nil, fmt.Errorf("key %q has profile without name or upstreams", key.Name)
			}
			resolved := make([]string, 0, len(profile.Upstreams))
			for _, upstreamName := range profile.Upstreams {
				upstream, ok := key.Upstreams[upstreamName]
				if !ok || strings.TrimSpace(upstream) == "" || strings.Contains(upstream, "|") {
					return nil, fmt.Errorf("key %q profile %q has invalid upstream reference %q", key.Name, name, upstreamName)
				}
				resolved = append(resolved, upstream)
			}
			for param := range profile.Params {
				if param == "url" || param == "chaintoken" || param == "coverprofile" || strings.TrimSpace(param) == "" {
					return nil, fmt.Errorf("key %q profile %q has reserved parameter %q", key.Name, name, param)
				}
			}
			profiles[name] = Profile{Upstreams: resolved, Params: profile.Params}
		}
		result.profiles[key.Name] = profiles
	}
	return result, nil
}

func (c *Config) Select(keyName, profileName string) (Profile, bool) {
	if c == nil {
		return Profile{}, false
	}
	profile, ok := c.profiles[keyName][profileName]
	if !ok {
		return Profile{}, false
	}
	selected := Profile{Upstreams: append([]string(nil), profile.Upstreams...), Params: make(map[string]string, len(profile.Params))}
	for name, value := range profile.Params {
		selected.Params[name] = value
	}
	return selected, true
}
