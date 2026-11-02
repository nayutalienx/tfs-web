package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type networkTestTransport struct {
	call   func(*http.Request) (*http.Response, error)
	resets int
}

func (t *networkTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return t.call(r)
}
func (t *networkTestTransport) CloseIdleConnections() { t.resets++ }

func networkTestClient(t *testing.T, call func(*http.Request) (*http.Response, error)) (*client, *networkTestTransport) {
	t.Helper()
	transport := &networkTestTransport{call: call}
	c := newClient(config{BaseURL: "https://tfs.example.invalid/collection", Project: "DemoProject", PAT: "synthetic-secret"})
	c.http.Transport = transport
	return c, transport
}

func networkTestResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestTFSReadReconnectsAndReplaysBody(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			calls := 0
			c, transport := networkTestClient(t, func(r *http.Request) (*http.Response, error) {
				calls++
				if _, pat, ok := r.BasicAuth(); !ok || pat != "synthetic-secret" {
					t.Fatal("authentication lost during reconnect")
				}
				if method == http.MethodPost {
					body, err := io.ReadAll(r.Body)
					if err != nil || string(body) != `{"query":"SELECT [System.Id] FROM WorkItems"}` {
						t.Fatalf("request body not replayed: %s, %v", body, err)
					}
				}
				if calls == 1 {
					return nil, &net.OpError{Op: "read", Net: "tcp", Err: errors.New("synthetic VPN disconnect")}
				}
				return networkTestResponse(`{"id":42}`), nil
			})
			path := "wit/workitems/42"
			var body any
			if method == http.MethodPost {
				path, body = "wit/wiql", map[string]string{"query": "SELECT [System.Id] FROM WorkItems"}
			}
			var item workItem
			if err := c.request(context.Background(), method, path, nil, body, &item); err != nil || item.ID != 42 || calls != 2 || transport.resets != 1 {
				t.Fatalf("item=%v calls=%d resets=%d err=%v", item, calls, transport.resets, err)
			}
		})
	}
}

func TestTFSReadRecoversAfterHeaderTimeout(t *testing.T) {
	calls := 0
	c, transport := networkTestClient(t, func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			<-r.Context().Done()
			return nil, r.Context().Err()
		}
		return networkTestResponse(`{"id":42}`), nil
	})
	c.http.Timeout = time.Second
	var item workItem
	if err := c.request(context.Background(), "GET", "wit/workitems/42", nil, nil, &item); err != nil || calls != 2 || item.ID != 42 || transport.resets != 1 {
		t.Fatalf("calls=%d resets=%d item=%v err=%v", calls, transport.resets, item, err)
	}
}

func TestTFSReadRetriesTruncatedResponse(t *testing.T) {
	calls := 0
	c, _ := networkTestClient(t, func(*http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return networkTestResponse(`{"id":`), nil
		}
		return networkTestResponse(`{"id":42}`), nil
	})
	var item workItem
	if err := c.request(context.Background(), "GET", "wit/workitems/42", nil, nil, &item); err != nil || calls != 2 || item.ID != 42 {
		t.Fatalf("calls=%d item=%v err=%v", calls, item, err)
	}
}

func TestTFSNetworkRetriesAreBounded(t *testing.T) {
	calls := 0
	c, transport := networkTestClient(t, func(*http.Request) (*http.Response, error) {
		calls++
		return nil, io.EOF
	})
	err := c.request(context.Background(), "GET", "git/repositories", nil, nil, nil)
	if err == nil || calls != 2 || transport.resets != 2 {
		t.Fatalf("calls=%d resets=%d err=%v", calls, transport.resets, err)
	}
	// A later browser request must still be able to recover without restart.
	transport.call = func(*http.Request) (*http.Response, error) {
		return networkTestResponse(`{"id":42}`), nil
	}
	if err := c.request(context.Background(), "GET", "wit/workitems/42", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestTFSWritesAreNeverRetried(t *testing.T) {
	for _, method := range []string{"POST", "PATCH", "PUT", "DELETE"} {
		t.Run(method, func(t *testing.T) {
			calls := 0
			c, transport := networkTestClient(t, func(*http.Request) (*http.Response, error) {
				calls++
				return nil, io.EOF
			})
			err := c.request(context.Background(), method, "wit/workitems/42", nil, map[string]string{"title": "Synthetic"}, nil)
			if err == nil || calls != 1 || transport.resets != 1 || !strings.Contains(err.Error(), "проверьте результат") {
				t.Fatalf("calls=%d resets=%d err=%v", calls, transport.resets, err)
			}
		})
	}
}

func TestTFSDoesNotRetryAPIErrorsOrInvalidTLS(t *testing.T) {
	for _, status := range []int{302, 401, 403, 404, 409, 429, 500, 503} {
		calls := 0
		c, transport := networkTestClient(t, func(*http.Request) (*http.Response, error) {
			calls++
			response := networkTestResponse("")
			response.StatusCode = status
			return response, nil
		})
		if err := c.request(context.Background(), "GET", "git/repositories", nil, nil, nil); err == nil || calls != 1 || transport.resets != 0 {
			t.Fatalf("status=%d calls=%d resets=%d err=%v", status, calls, transport.resets, err)
		}
	}
	calls := 0
	c, transport := networkTestClient(t, func(*http.Request) (*http.Response, error) {
		calls++
		return nil, &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}
	})
	if err := c.request(context.Background(), "GET", "git/repositories", nil, nil, nil); err == nil || calls != 1 || transport.resets != 0 {
		t.Fatalf("calls=%d resets=%d err=%v", calls, transport.resets, err)
	}
}

func TestTFSNetworkRecoveryHonorsCancellation(t *testing.T) {
	for _, duringBackoff := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		c, _ := networkTestClient(t, func(*http.Request) (*http.Response, error) {
			calls++
			cancel()
			if duringBackoff {
				return nil, io.EOF
			}
			return nil, context.Canceled
		})
		err := c.request(ctx, "GET", "git/repositories", nil, nil, nil)
		if err == nil || calls != 1 {
			t.Fatalf("calls=%d err=%v", calls, err)
		}
	}
}

func TestTFSTransportReplacesPool(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, r.RemoteAddr)
	}))
	defer upstream.Close()
	transport := &reconnectingTransport{}
	defer transport.CloseIdleConnections()
	h := &http.Client{Transport: transport}
	fetch := func() string {
		t.Helper()
		response, err := h.Get(upstream.URL)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		b, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	first := fetch()
	if second := fetch(); second != first {
		t.Fatal("healthy connection was not reused")
	}
	transport.CloseIdleConnections()
	if fresh := fetch(); fresh == first {
		t.Fatal("stale connection survived pool replacement")
	}
}
