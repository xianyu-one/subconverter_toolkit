package mihomo

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

type Port struct{ Value *int }

func (p *Port) UnmarshalJSON(b []byte) error {
	if bytes.Equal(b, []byte("null")) || bytes.Equal(b, []byte(`""`)) {
		return nil
	}
	var s string
	if len(b) > 0 && b[0] == '"' {
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
	} else {
		s = string(b)
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 || n > 65535 {
		return fmt.Errorf("invalid port")
	}
	p.Value = &n
	return nil
}

type Metadata struct {
	SourceIP         string `json:"sourceIP"`
	SourcePort       Port   `json:"sourcePort"`
	DestinationIP    string `json:"destinationIP"`
	DestinationPort  Port   `json:"destinationPort"`
	Host             string `json:"host"`
	SniffHost        string `json:"sniffHost"`
	DestinationIPASN string `json:"destinationIPASN"`
	Network          string `json:"network"`
	Type             string `json:"type"`
	DNSMode          string `json:"dnsMode"`
	Process          string `json:"process"`
	ProcessPath      string `json:"processPath"`
}
type Connection struct {
	ID             string   `json:"id"`
	Start          string   `json:"start"`
	Metadata       Metadata `json:"metadata"`
	Upload         int64    `json:"upload"`
	Download       int64    `json:"download"`
	Rule           string   `json:"rule"`
	RulePayload    string   `json:"rulePayload"`
	Chains         []string `json:"chains"`
	ProviderChains []string `json:"providerChains"`
	StartedAt      *int64   `json:"-"`
	IPVersion      int      `json:"-"`
	TargetKind     string   `json:"-"`
	TargetValue    string   `json:"-"`
	TargetSource   string   `json:"-"`
}
type Frame struct {
	Connections   []Connection
	ReceivedAt    int64
	UploadTotal   *int64
	DownloadTotal *int64
}

func DecodeFrame(b []byte, at time.Time) (Frame, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(b, &top); err != nil {
		return Frame{}, err
	}
	raw, ok := top["connections"]
	if !ok {
		return Frame{}, errors.New("connections field missing")
	}
	if !bytes.Equal(raw, []byte("null")) && (len(raw) == 0 || raw[0] != '[') {
		return Frame{}, errors.New("connections must be array or null")
	}
	var cs []Connection
	if err := json.Unmarshal(raw, &cs); err != nil {
		return Frame{}, err
	}
	if len(cs) > 100000 {
		return Frame{}, errors.New("too many connections")
	}
	seen := make(map[string]bool, len(cs))
	for i := range cs {
		c := &cs[i]
		if c.ID == "" || seen[c.ID] {
			return Frame{}, errors.New("missing or duplicate connection id")
		}
		seen[c.ID] = true
		if c.Upload < 0 || c.Download < 0 {
			return Frame{}, errors.New("negative byte counter")
		}
		if c.Start != "" {
			t, e := time.Parse(time.RFC3339Nano, c.Start)
			if e != nil {
				return Frame{}, fmt.Errorf("invalid start: %w", e)
			}
			x := t.UnixMilli()
			c.StartedAt = &x
		}
		if c.Metadata.DestinationIP != "" {
			a, e := netip.ParseAddr(c.Metadata.DestinationIP)
			if e == nil {
				a = a.Unmap()
				c.Metadata.DestinationIP = a.String()
				if a.Is4() {
					c.IPVersion = 4
				} else {
					c.IPVersion = 6
				}
			} else {
				c.Metadata.DestinationIP = ""
			}
		}
		c.TargetKind = "unknown"
		c.TargetSource = "unknown"
		if h := strings.TrimSpace(c.Metadata.Host); h != "" {
			c.TargetKind = "domain"
			c.TargetSource = "host"
			c.TargetValue = strings.ToLower(strings.TrimSuffix(h, "."))
		} else if h := strings.TrimSpace(c.Metadata.SniffHost); h != "" {
			c.TargetKind = "domain"
			c.TargetSource = "sniff_host"
			c.TargetValue = strings.ToLower(strings.TrimSuffix(h, "."))
		} else if c.Metadata.DestinationIP != "" {
			c.TargetKind = "ip_only"
			c.TargetSource = "ip"
			c.TargetValue = c.Metadata.DestinationIP
		}
	}
	f := Frame{Connections: cs, ReceivedAt: at.UnixMilli()}
	if v, ok := top["uploadTotal"]; ok && string(v) != "null" {
		var n int64
		if json.Unmarshal(v, &n) == nil && n >= 0 {
			f.UploadTotal = &n
		}
	}
	if v, ok := top["downloadTotal"]; ok && string(v) != "null" {
		var n int64
		if json.Unmarshal(v, &n) == nil && n >= 0 {
			f.DownloadTotal = &n
		}
	}
	return f, nil
}
