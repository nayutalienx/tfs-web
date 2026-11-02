package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPageTimings(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Millisecond)
		if strings.HasSuffix(r.URL.Path, "/wiql") {
			fmt.Fprint(w, `{"workItems":[{"id":42}]}`)
		} else {
			fmt.Fprint(w, `{"value":[{"id":42,"fields":{"System.Title":"Пример"}}]}`)
		}
	}))
	defer upstream.Close()
	s := &server{c: newClient(config{BaseURL: upstream.URL, Project: "DemoProject", PAT: "test-secret"}), templates: tmpl}
	for _, tc := range []struct {
		path  string
		calls string
	}{
		{"/search?q=example", "Запросов: 2, с ошибкой: 0"},
		{"/search", "Запросов: 0, с ошибкой: 0"},
	} {
		rec := appRequest(s.routes(), "GET", tc.path, nil)
		if rec.Code != 200 {
			t.Fatalf("status=%d", rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, `class="connection-stats"`) || !strings.Contains(body, tc.calls) || !strings.Contains(body, "data-page-time") {
			t.Fatalf("missing timing bar or counts for %s", tc.path)
		}
		if !strings.Contains(rec.Header().Get("Server-Timing"), "tfs;dur=") || strings.Contains(body, "test-secret") {
			t.Fatal("missing timing header or exposed secret")
		}
	}
	rec := appRequest(s.routes(), "GET", "/static/timings.js", nil)
	if rec.Code != 200 || rec.Body.String() != string(timingsScript) {
		t.Fatal("timing script unavailable")
	}
}

func TestTimingIsolationAndErrors(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer upstream.Close()
	c := newClient(config{BaseURL: upstream.URL, Project: "DemoProject", PAT: "synthetic"})
	h := withPageTimings(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next := withPageTimings(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/error" {
				_ = c.request(r.Context(), "GET", "example", nil, nil, nil)
			}
			timings := r.Context().Value(timingKey{}).(*requestTimings)
			p := page{}
			w.(*timingWriter).pageTimings(&p)
			if r.URL.Path == "/error" {
				if p.TFSCalls != 1 || p.TFSErrors != 1 || timings.tfs <= 0 {
					t.Errorf("failed request not measured: %+v", p)
				}
			} else if p.TFSCalls != 0 || p.TFSErrors != 0 {
				t.Error("timings leaked between requests")
			}
		}))
		next.ServeHTTP(w, r)
	}))
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			path := "/empty"
			if i%2 == 0 {
				path = "/error"
			}
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", path, nil))
		}(i)
	}
	wg.Wait()
}

func TestTimingRecordConcurrent(t *testing.T) {
	timings := &requestTimings{started: time.Now()}
	ctx := context.WithValue(context.Background(), timingKey{}, timings)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx.Value(timingKey{}).(*requestTimings).record(time.Now().Add(-time.Millisecond), nil)
		}()
	}
	wg.Wait()
	if timings.calls != 20 || timings.tfs < 20*time.Millisecond {
		t.Fatal("lost concurrent timing measurements")
	}
}
