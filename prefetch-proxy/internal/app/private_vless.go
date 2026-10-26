package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
	"prefetch-proxy/internal/privateconfig"
)

type privateVLESSKey struct{}

// The converter can re-emit a VLESS source as VMess and discard VLESS-only
// fields. Carry only the selected nodes through the request and restore them
// after conversion, leaving the converter's final display names intact.
func withPrivateVLESS(r *http.Request, nodes []privateconfig.Node) *http.Request {
	var vless []privateconfig.Node
	for _, node := range nodes {
		if strings.EqualFold(fmt.Sprint(node["type"]), "vless") {
			vless = append(vless, node)
		}
	}
	if len(vless) == 0 || r.URL.Path != "/sub" || r.URL.Query().Get("target") != "clash" {
		return r
	}
	r = r.WithContext(context.WithValue(r.Context(), privateVLESSKey{}, vless))
	// Let Go's transport request and transparently decode gzip, if supported.
	r.Header.Del("Accept-Encoding")
	return r
}

func restorePrivateVLESSResponse(resp *http.Response) error {
	nodes, ok := resp.Request.Context().Value(privateVLESSKey{}).([]privateconfig.Node)
	if !ok || resp.StatusCode != http.StatusOK || len(nodes) == 0 {
		return nil
	}
	const maxConvertedSize = 32 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxConvertedSize+1))
	if err != nil {
		return err
	}
	if len(body) > maxConvertedSize {
		return fmt.Errorf("converted subscription exceeds size limit")
	}
	patched, err := restorePrivateVLESS(body, nodes)
	if err != nil {
		return err
	}
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(patched))
	resp.ContentLength = int64(len(patched))
	resp.Header.Set("Content-Length", strconv.Itoa(len(patched)))
	resp.Header.Del("ETag")
	resp.Header.Del("Content-MD5")
	return nil
}

func restorePrivateVLESS(body []byte, nodes []privateconfig.Node) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parse converted subscription: %w", err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("converted subscription is not a YAML mapping")
	}
	var proxies *yaml.Node
	root := doc.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "proxies" || root.Content[i].Value == "Proxy" {
			proxies = root.Content[i+1]
			break
		}
	}
	if proxies == nil || proxies.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("converted subscription has no proxy list")
	}
	used := make(map[int]bool)
	for _, original := range nodes {
		identity := nodeIdentity(original)
		match := -1
		for i, candidate := range proxies.Content {
			if used[i] || candidate.Kind != yaml.MappingNode || yamlIdentity(candidate) != identity {
				continue
			}
			if match != -1 {
				return nil, fmt.Errorf("ambiguous converted private VLESS node %q", original["name"])
			}
			match = i
		}
		if match == -1 {
			return nil, fmt.Errorf("converted private VLESS node %q is missing", original["name"])
		}
		name := yamlField(proxies.Content[match], "name")
		copy := make(privateconfig.Node, len(original))
		for key, value := range original {
			copy[key] = value
		}
		copy["name"] = name
		var replacement yaml.Node
		if err := replacement.Encode(copy); err != nil {
			return nil, err
		}
		proxies.Content[match] = &replacement
		used[match] = true
	}
	return yaml.Marshal(&doc)
}

func nodeIdentity(node privateconfig.Node) string {
	return fmt.Sprint(node["server"]) + "\x00" + fmt.Sprint(node["port"]) + "\x00" + fmt.Sprint(node["uuid"])
}

func yamlIdentity(node *yaml.Node) string {
	return yamlField(node, "server") + "\x00" + yamlField(node, "port") + "\x00" + yamlField(node, "uuid")
}

func yamlField(node *yaml.Node, field string) string {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == field {
			return node.Content[i+1].Value
		}
	}
	return ""
}
