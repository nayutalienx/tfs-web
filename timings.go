package main

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"
)

type timingKey struct{}

// Timings belong to one incoming request, never to a shared TFS client.
type requestTimings struct {
	started time.Time
	mu      sync.Mutex
	tfs     time.Duration
	calls   int
	errors  int
}

func (t *requestTimings) record(start time.Time, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.tfs += time.Since(start)
	t.calls++
	if err != nil {
		t.errors++
	}
}

type timingWriter struct {
	http.ResponseWriter
	timings *requestTimings
}

func (w *timingWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func withPageTimings(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Context().Value(timingKey{}) != nil {
			next.ServeHTTP(w, r)
			return
		}
		t := &requestTimings{started: time.Now()}
		next.ServeHTTP(&timingWriter{ResponseWriter: w, timings: t}, r.WithContext(context.WithValue(r.Context(), timingKey{}, t)))
	})
}

func durationLabel(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%d мс", d.Milliseconds())
	}
	return fmt.Sprintf("%.2f с", d.Seconds())
}

func (w *timingWriter) pageTimings(p *page) {
	t := w.timings
	t.mu.Lock()
	defer t.mu.Unlock()
	elapsed := time.Since(t.started)
	p.PageTime = durationLabel(elapsed)
	p.TFSTime = durationLabel(t.tfs)
	p.TFSCalls, p.TFSErrors = t.calls, t.errors
	w.Header().Set("Server-Timing", fmt.Sprintf("app;dur=%.2f, tfs;dur=%.2f", float64(elapsed)/float64(time.Millisecond), float64(t.tfs)/float64(time.Millisecond)))
}
