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
	Problems(context.Context, int64) ([]map[string]any, error)
	Detail(context.Context, string, string) (map[string]any, error)
	DailyHistory(context.Context, string, string, int64) ([]map[string]any, error)
	ProblemHistory(context.Context, string, string, int64, int64) ([]map[string]any, error)
	Close() error
}
