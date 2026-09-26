package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"mihomo-observer/internal/collector"
	"mihomo-observer/internal/geo"
	"mihomo-observer/internal/storage"
)

type Server struct {
	store  storage.Store
	status func() collector.Status
	pass   [32]byte
	geo    *geo.Resolver
}

//go:embed web/*
var webFiles embed.FS

func New(store storage.Store, password string, status func() collector.Status, locator ...*geo.Resolver) *Server {
	s := &Server{store: store, status: status, pass: sha256.Sum256([]byte(password))}
	if len(locator) > 0 {
		s.geo = locator[0]
	}
	return s
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	assets, _ := fs.Sub(webFiles, "web")
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(assets))))
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		http.ServeFileFS(w, r, assets, "index.html")
	})
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"collector": s.status(), "time_ms": time.Now().UnixMilli()})
	})
	mux.HandleFunc("GET /api/dashboard", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.store.Dashboard(r.Context(), time.Now().UnixMilli())
		if e == nil {
			applyLiveInterruption(v, s.status())
		}
		respond(w, v, e)
	})
	mux.HandleFunc("GET /api/problems", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("page") {
			o, ok := parsePage(w, r, map[string]bool{"last_seen": true, "target": true, "kind": true, "severity": true, "confidence": true, "samples": true}, "severity")
			if !ok {
				return
			}
			v, e := s.store.ProblemsPage(r.Context(), time.Now().UnixMilli(), o)
			respond(w, v, e)
			return
		}
		v, e := s.store.Problems(r.Context(), time.Now().UnixMilli())
		respond(w, v, e)
	})
	mux.HandleFunc("GET /api/targets", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("page") {
			o, ok := parsePage(w, r, map[string]bool{"last_seen": true, "name": true, "kind": true, "connections": true, "bytes": true}, "last_seen")
			if !ok {
				return
			}
			v, e := s.store.TargetsPage(r.Context(), o)
			respond(w, v, e)
			return
		}
		v, e := s.store.Targets(r.Context())
		respond(w, v, e)
	})
	mux.HandleFunc("GET /api/flows", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		now := time.Now().UnixMilli()
		o := storage.FlowOptions{Live: q.Get("mode") == "live", Start: now - 86400000, End: now, Target: q.Get("target")}
		if mode := q.Get("mode"); mode != "" && mode != "live" && mode != "history" {
			http.Error(w, "invalid mode", 400)
			return
		}
		if len(o.Target) > 255 {
			http.Error(w, "invalid target", 400)
			return
		}
		if raw := q.Get("start_ms"); raw != "" {
			n, e := strconv.ParseInt(raw, 10, 64)
			if e != nil {
				http.Error(w, "invalid start_ms", 400)
				return
			}
			o.Start = n
		}
		if raw := q.Get("end_ms"); raw != "" {
			n, e := strconv.ParseInt(raw, 10, 64)
			if e != nil {
				http.Error(w, "invalid end_ms", 400)
				return
			}
			o.End = n
		}
		if o.End <= o.Start || o.Start < 0 || o.End-o.Start > 366*86400000 {
			http.Error(w, "invalid time range", 400)
			return
		}
		v, e := s.store.Flows(r.Context(), o)
		if e == nil {
			e = s.annotateFlows(r.Context(), v)
		}
		if e == nil && o.Live {
			applyLiveInterruption(v, s.status())
		}
		respond(w, v, e)
	})
	mux.HandleFunc("GET /api/flow-samples", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		target := q.Get("target")
		if target == "" || len(target) > 255 {
			http.Error(w, "invalid target", 400)
			return
		}
		route, e1 := strconv.ParseInt(q.Get("route_id"), 10, 64)
		start, e2 := strconv.ParseInt(q.Get("start_ms"), 10, 64)
		end, e3 := strconv.ParseInt(q.Get("end_ms"), 10, 64)
		page, e4 := strconv.Atoi(q.Get("page"))
		if e1 != nil || e2 != nil || e3 != nil || (q.Get("page") != "" && e4 != nil) || route < 1 || start < 0 || end <= start || end-start > 366*86400000 || page < 0 || page > 20000 {
			http.Error(w, "invalid sample query", 400)
			return
		}
		if page == 0 {
			page = 1
		}
		sort := q.Get("sort")
		if sort == "" {
			sort = "time"
		}
		if !map[string]bool{"time": true, "bytes": true, "state": true, "ip": true}[sort] || q.Get("dir") != "" && q.Get("dir") != "asc" && q.Get("dir") != "desc" {
			http.Error(w, "invalid sample sort", 400)
			return
		}
		v, e := s.store.FlowSamples(r.Context(), target, route, start, end, storage.PageOptions{Sort: sort, Desc: q.Get("dir") != "asc", Limit: 50, Offset: (page - 1) * 50})
		respond(w, v, e)
	})
	mux.HandleFunc("GET /api/details/{kind}/{value}", func(w http.ResponseWriter, r *http.Request) {
		kind, value := r.PathValue("kind"), r.PathValue("value")
		if len(value) > 255 || strings.ContainsAny(value, "\x00\n\r") {
			http.Error(w, "invalid target", 400)
			return
		}
		v, e := s.store.Detail(r.Context(), kind, value)
		respond(w, v, e)
	})
	mux.HandleFunc("GET /api/history/{kind}/{value}", func(w http.ResponseWriter, r *http.Request) {
		kind, value := r.PathValue("kind"), r.PathValue("value")
		if len(value) > 255 || strings.ContainsAny(value, "\x00\n\r") {
			http.Error(w, "invalid target", 400)
			return
		}
		before := int64(1<<63 - 1)
		if raw := r.URL.Query().Get("before_ms"); raw != "" {
			var err error
			before, err = strconv.ParseInt(raw, 10, 64)
			if err != nil || before <= 0 {
				http.Error(w, "invalid before_ms", 400)
				return
			}
		}
		v, err := s.store.DailyHistory(r.Context(), kind, value, before)
		respond(w, v, err)
	})
	mux.HandleFunc("GET /api/problem-history/{kind}/{value}", func(w http.ResponseWriter, r *http.Request) {
		kind, value := r.PathValue("kind"), r.PathValue("value")
		if len(value) > 255 || strings.ContainsAny(value, "\x00\n\r") {
			http.Error(w, "invalid target", 400)
			return
		}
		start, err1 := strconv.ParseInt(r.URL.Query().Get("start_ms"), 10, 64)
		end, err2 := strconv.ParseInt(r.URL.Query().Get("end_ms"), 10, 64)
		if err1 != nil || err2 != nil || start < 0 || end <= start || end-start > 3650*86400000 {
			http.Error(w, "invalid time range", 400)
			return
		}
		v, err := s.store.ProblemHistory(r.Context(), kind, value, start, end)
		respond(w, v, err)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		user, password, ok := r.BasicAuth()
		sum := sha256.Sum256([]byte(password))
		if !ok || user != "admin" || subtle.ConstantTimeCompare(sum[:], s.pass[:]) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="Mihomo Observer"`)
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) annotateFlows(ctx context.Context, data map[string]any) error {
	items, ok := data["items"].([]map[string]any)
	if !ok {
		return nil
	}
	if s.geo != nil {
		data["origin"] = s.geo.Origin()
		data["geoip_version"] = s.geo.Version()
		data["geoip_error"] = s.geo.Problem()
	}
	for _, item := range items {
		if s.geo != nil {
			if ip, ok := item["destination_ip"].(string); ok {
				item["target_point"] = s.geo.IP(ip)
			}
		}
		chains, ok := item["chains"].([]string)
		if !ok || len(chains) == 0 {
			continue
		}
		at, ok := item["last_seen_at_ms"].(int64)
		if !ok {
			continue
		}
		if s.geo != nil {
			hops := make([]map[string]any, 0, len(chains))
			for i := len(chains) - 1; i >= 0; i-- {
				hops = append(hops, map[string]any{"name": chains[i], "locations": s.geo.Node(chains[i])})
			}
			item["hops"] = hops
		}
		topology, snapshotAt, valid, err := s.store.TopologyAt(ctx, at)
		if err != nil {
			return err
		}
		if snapshotAt == 0 || at-valid > 120000 {
			continue
		}
		if hops, ok := item["hops"].([]map[string]any); ok {
			for _, hop := range hops {
				if name, ok := hop["name"].(string); ok {
					hop["type"] = topology[name].Type
				}
			}
		}
		proxy := topology[chains[0]]
		if proxy.DialerProxy == "" {
			continue
		}
		item["dialer_proxy"] = proxy.DialerProxy
		item["topology_at_ms"] = snapshotAt
		item["topology_last_valid_ms"] = valid
		upstream := topology[proxy.DialerProxy]
		if len(upstream.All) > 0 {
			item["first_hop_candidates"] = upstream.All
		} else {
			item["first_hop_candidates"] = []string{proxy.DialerProxy}
		}
		item["first_hop_observed"] = false
	}
	return nil
}

func parsePage(w http.ResponseWriter, r *http.Request, allowed map[string]bool, fallback string) (storage.PageOptions, bool) {
	q := r.URL.Query()
	o := storage.PageOptions{Sort: q.Get("sort"), Desc: q.Get("dir") != "asc", Limit: 50}
	if o.Sort == "" {
		o.Sort = fallback
	}
	if !allowed[o.Sort] || (q.Get("dir") != "" && q.Get("dir") != "asc" && q.Get("dir") != "desc") {
		http.Error(w, "invalid sort", 400)
		return o, false
	}
	if raw := q.Get("page"); raw != "" {
		n, e := strconv.Atoi(raw)
		if e != nil || n < 1 || n > 20000 {
			http.Error(w, "invalid page", 400)
			return o, false
		}
		o.Offset = (n - 1) * o.Limit
	}
	return o, true
}

func applyLiveInterruption(dashboard map[string]any, status collector.Status) {
	last := status.LastCommitMS
	if last == 0 {
		var previous int64
		if value, ok := dashboard["as_of_ms"].(int64); ok {
			previous = value
		} else if value, ok := dashboard["last_connections_at_ms"].(int64); ok {
			previous = value
		}
		if previous > 0 {
			delete(dashboard, "current_connections")
			dashboard["interruption_reason"] = "observer_restart"
			dashboard["interruption_at_ms"] = previous
		}
	}
	if status.LastReadErrorMS > last {
		delete(dashboard, "current_connections")
		dashboard["interruption_reason"] = status.LastReadError
		dashboard["interruption_at_ms"] = status.LastReadErrorMS
	}
	if status.LastDropMS > last {
		delete(dashboard, "current_connections")
		dashboard["interruption_reason"] = "mailbox_overflow"
		dashboard["interruption_at_ms"] = status.LastDropMS
	}
	if status.LastWriteErrorMS > last {
		delete(dashboard, "current_connections")
		dashboard["interruption_reason"] = "writer_failed"
		dashboard["interruption_at_ms"] = status.LastWriteErrorMS
	}
}
func write(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}
func respond(w http.ResponseWriter, v any, e error) {
	if e != nil {
		http.Error(w, "query failed", 500)
		return
	}
	write(w, v)
}
func Serve(ctx context.Context, addr string, h http.Handler) error {
	srv := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second}
	done := make(chan struct{})
	go func() {
		<-ctx.Done()
		stop, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(stop)
		close(done)
	}()
	err := srv.ListenAndServe()
	if err == http.ErrServerClosed {
		<-done
		return nil
	}
	return err
}
