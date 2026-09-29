package main

import (
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type source struct {
	name    string
	file    string
	url     string
	enabled bool
	mmdb    bool
}

type config struct {
	outputDir string
	interval  time.Duration
	timeout   time.Duration
	maxBytes  int64
	proxy     string
	once      bool
	sources   []source
}

func parseConfig(args []string) (config, error) {
	var cfg config
	fs := flag.NewFlagSet("geox-sync", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var applyEnv []func() error
	flagProvided := func(name string) bool {
		for _, arg := range args {
			if arg == "--" {
				break
			}
			if arg == "--"+name || arg == "-"+name || strings.HasPrefix(arg, "--"+name+"=") || strings.HasPrefix(arg, "-"+name+"=") {
				return true
			}
		}
		return false
	}

	stringFlag := func(name, env, fallback, help string) *string {
		value := fs.String(name, fallback, help)
		applyEnv = append(applyEnv, func() error {
			if raw, ok := os.LookupEnv(env); ok && !flagProvided(name) {
				*value = raw
			}
			return nil
		})
		return value
	}
	boolFlag := func(name, env string, fallback bool, help string) *bool {
		value := fs.Bool(name, fallback, help)
		applyEnv = append(applyEnv, func() error {
			if raw, ok := os.LookupEnv(env); ok && !flagProvided(name) {
				parsed, err := strconv.ParseBool(raw)
				if err != nil {
					return fmt.Errorf("%s: %w", env, err)
				}
				*value = parsed
			}
			return nil
		})
		return value
	}
	durationFlag := func(name, env string, fallback time.Duration, help string) *time.Duration {
		value := fs.Duration(name, fallback, help)
		applyEnv = append(applyEnv, func() error {
			if raw, ok := os.LookupEnv(env); ok && !flagProvided(name) {
				parsed, err := time.ParseDuration(raw)
				if err != nil {
					return fmt.Errorf("%s: %w", env, err)
				}
				*value = parsed
			}
			return nil
		})
		return value
	}

	outputDir := stringFlag("output-dir", "GEOX_SYNC_OUTPUT_DIR", "./data", "directory for downloaded files")
	interval := durationFlag("interval", "GEOX_SYNC_INTERVAL", 24*time.Hour, "time between completed sync rounds")
	timeout := durationFlag("timeout", "GEOX_SYNC_TIMEOUT", 2*time.Minute, "timeout for each download")
	maxBytes := fs.Int64("max-bytes", 512<<20, "maximum bytes per downloaded file")
	applyEnv = append(applyEnv, func() error {
		if raw, ok := os.LookupEnv("GEOX_SYNC_MAX_BYTES"); ok && !flagProvided("max-bytes") {
			parsed, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				return fmt.Errorf("GEOX_SYNC_MAX_BYTES: %w", err)
			}
			*maxBytes = parsed
		}
		return nil
	})
	proxy := stringFlag("proxy", "GEOX_SYNC_PROXY", "", "proxy URL (http, https, socks5, socks5h)")
	once := boolFlag("once", "GEOX_SYNC_ONCE", false, "run one sync round and exit")

	definitions := []source{
		{name: "geoip", file: "geoip.dat", url: "https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geoip.dat"},
		{name: "geosite", file: "geosite.dat", url: "https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geosite.dat"},
		{name: "mmdb", file: "country.mmdb", url: "https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/country.mmdb", mmdb: true},
		{name: "asn", file: "GeoLite2-ASN.mmdb", url: "https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/GeoLite2-ASN.mmdb", mmdb: true},
	}
	type sourceFlags struct {
		url     *string
		enabled *bool
	}
	flags := make([]sourceFlags, len(definitions))
	for i, item := range definitions {
		prefix := "GEOX_SYNC_" + strings.ToUpper(item.name)
		flags[i].url = stringFlag(item.name+"-url", prefix+"_URL", item.url, item.name+" source URL")
		flags[i].enabled = boolFlag(item.name+"-enabled", prefix+"_ENABLED", true, "enable "+item.name+" download")
	}
	if err := fs.Parse(args); err != nil {
		return cfg, err
	}
	for _, resolve := range applyEnv {
		if err := resolve(); err != nil {
			return cfg, err
		}
	}
	if fs.NArg() != 0 {
		return cfg, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if strings.TrimSpace(*outputDir) == "" {
		return cfg, errors.New("output directory must not be empty")
	}
	if *interval <= 0 || *timeout <= 0 || *maxBytes <= 0 {
		return cfg, errors.New("interval, timeout and max-bytes must be positive")
	}
	if *maxBytes == int64(^uint64(0)>>1) {
		return cfg, errors.New("max-bytes is too large")
	}
	if *proxy != "" {
		parsed, err := url.Parse(*proxy)
		if err != nil || parsed.Hostname() == "" || parsed.Port() == "" {
			return cfg, errors.New("proxy must be a URL with host and port")
		}
		switch parsed.Scheme {
		case "http", "https", "socks5", "socks5h":
		default:
			return cfg, errors.New("proxy scheme must be http, https, socks5 or socks5h")
		}
	}
	for i, item := range definitions {
		item.url = *flags[i].url
		item.enabled = *flags[i].enabled
		if item.enabled {
			parsed, err := url.Parse(item.url)
			if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
				return cfg, fmt.Errorf("%s source must be an HTTP(S) URL", item.name)
			}
		}
		cfg.sources = append(cfg.sources, item)
	}
	cfg.outputDir, cfg.interval, cfg.timeout = *outputDir, *interval, *timeout
	cfg.maxBytes, cfg.proxy, cfg.once = *maxBytes, *proxy, *once
	return cfg, nil
}
