package storage

import (
	"context"
	"mihomo-observer/internal/mihomo"
)

type Store interface {
	ApplyFrame(context.Context, mihomo.Frame, bool) error
	RecordGap(context.Context, string, string, int64, int64, int64) error
	SetControllerVersion(context.Context, string, bool) error
	Project(context.Context, int64) error
	Analyze(context.Context, int64) error
	Cleanup(context.Context, int64, int, int, int) error
	Dashboard(context.Context, int64) (map[string]any, error)
	Targets(context.Context) ([]map[string]any, error)
	TargetsPage(context.Context, PageOptions) (Page, error)
	Problems(context.Context, int64) ([]map[string]any, error)
	ProblemsPage(context.Context, int64, PageOptions) (Page, error)
	RecordTopology(context.Context, int64, map[string]ProxyInfo) error
	TopologyAt(context.Context, int64) (map[string]ProxyInfo, int64, int64, error)
	Flows(context.Context, FlowOptions) (map[string]any, error)
	FlowSamples(context.Context, string, int64, int64, int64, PageOptions) (Page, error)
	Detail(context.Context, string, string) (map[string]any, error)
	DailyHistory(context.Context, string, string, int64) ([]map[string]any, error)
	ProblemHistory(context.Context, string, string, int64, int64) ([]map[string]any, error)
	Close() error
}

type PageOptions struct {
	Sort   string
	Desc   bool
	Limit  int
	Offset int
}

type Page struct {
	Items  []map[string]any `json:"items"`
	Total  int64            `json:"total"`
	Limit  int              `json:"limit"`
	Offset int              `json:"offset"`
}

type ProxyInfo struct {
	Type        string   `json:"type"`
	DialerProxy string   `json:"dialer_proxy,omitempty"`
	Now         string   `json:"now,omitempty"`
	All         []string `json:"all,omitempty"`
}

type FlowOptions struct {
	Live   bool
	Start  int64
	End    int64
	Target string
}
