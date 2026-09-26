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
	"mihomo-observer/internal/storage"
)

type Server struct {
	store  storage.Store
	status func() collector.Status
	pass   [32]byte
}

//go:embed web/*
var webFiles embed.FS

func New(store storage.Store, password string, status func() collector.Status) *Server {
	return &Server{store: store, status: status, pass: sha256.Sum256([]byte(password))}
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
		v, e := s.store.Problems(r.Context(), time.Now().UnixMilli())
		respond(w, v, e)
	})
	mux.HandleFunc("GET /api/targets", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.store.Targets(r.Context())
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

func applyLiveInterruption(dashboard map[string]any, status collector.Status) {
	last := status.LastCommitMS
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
