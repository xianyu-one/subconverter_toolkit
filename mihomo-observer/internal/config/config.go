package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Mihomo struct {
		API    string `yaml:"api"`
		Secret string `yaml:"secret"`
	} `yaml:"mihomo"`
	Database struct {
		Path string `yaml:"path"`
	} `yaml:"database"`
	Collector struct {
		Enabled    *bool `yaml:"enabled"`
		IntervalMS int   `yaml:"interval_ms"`
	} `yaml:"collector"`
	Analyzer struct {
		Enabled *bool `yaml:"enabled"`
	} `yaml:"analyzer"`
	Retention struct {
		RawDays    int `yaml:"raw_connections_days"`
		HourlyDays int `yaml:"hourly_stats_days"`
		DailyDays  int `yaml:"daily_stats_days"`
	} `yaml:"retention"`
	ReportingTimezone string `yaml:"reporting_timezone"`
	Web               struct {
		Listen   string `yaml:"listen"`
		Password string `yaml:"password"`
	} `yaml:"web"`
}

func Load(path string) (Config, error) {
	var c Config
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return c, err
	}
	if c.Mihomo.API == "" {
		return c, errors.New("mihomo.api is required")
	}
	u, err := url.Parse(c.Mihomo.API)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return c, errors.New("mihomo.api must be an http(s) base URL without credentials or query")
	}
	if c.Database.Path == "" {
		return c, errors.New("database.path is required")
	}
	if c.ReportingTimezone == "" {
		return c, errors.New("reporting_timezone is required")
	}
	if _, err := time.LoadLocation(c.ReportingTimezone); err != nil {
		return c, fmt.Errorf("reporting_timezone: %w", err)
	}
	if c.Web.Listen == "" {
		c.Web.Listen = "127.0.0.1:8080"
	}
	if c.Web.Password == "" {
		return c, errors.New("web.password is required for every listener")
	}
	if c.Collector.IntervalMS == 0 {
		c.Collector.IntervalMS = 1000
	}
	if c.Collector.IntervalMS < 500 || c.Collector.IntervalMS > 60000 {
		return c, errors.New("collector.interval_ms must be 500..60000")
	}
	if c.Retention.RawDays == 0 {
		c.Retention.RawDays = 14
	}
	if c.Retention.HourlyDays == 0 {
		c.Retention.HourlyDays = 180
	}
	if c.Retention.RawDays < 0 || c.Retention.HourlyDays < 0 || c.Retention.DailyDays < 0 {
		return c, errors.New("retention days cannot be negative")
	}
	return c, nil
}

func (c Config) CollectEnabled() bool { return c.Collector.Enabled == nil || *c.Collector.Enabled }
func (c Config) AnalyzeEnabled() bool { return c.Analyzer.Enabled == nil || *c.Analyzer.Enabled }
