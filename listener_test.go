package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDedicatedPortHostGuard(t *testing.T) {
	app := testApp(t, func(w http.ResponseWriter, r *http.Request) { t.Fatal("unexpected TFS request") })
	for _, host := range []string{listenAddress, "localhost:18763", "127.0.0.1:8080", "localhost:8080", "evil.example:18763"} {
		r := httptest.NewRequest("GET", "http://"+host+"/", nil)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		want := http.StatusForbidden
		if host == listenAddress || host == "localhost:18763" {
			want = http.StatusSeeOther
		}
		if w.Code != want {
			t.Errorf("Host %s: got %d want %d", host, w.Code, want)
		}
	}
}
