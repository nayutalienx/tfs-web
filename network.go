package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

const tfsRequestTimeout = 30 * time.Second
const tfsRetryDelay = 250 * time.Millisecond

// Browser contexts share connections, not credentials. Replacing the transport
// drops stale VPN routes and HTTP/2 sessions for subsequent requests as well.
var tfsConnections = &reconnectingTransport{}

type reconnectingTransport struct {
	mu      sync.Mutex
	current *http.Transport
}

func newTFSTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DialContext = (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 15 * time.Second}).DialContext
	t.TLSHandshakeTimeout = 5 * time.Second
	t.ResponseHeaderTimeout = 10 * time.Second
	t.IdleConnTimeout = 15 * time.Second
	return t
}

func (t *reconnectingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.mu.Lock()
	if t.current == nil {
		t.current = newTFSTransport()
	}
	current := t.current
	t.mu.Unlock()
	return current.RoundTrip(r)
}

func (t *reconnectingTransport) CloseIdleConnections() {
	t.mu.Lock()
	old := t.current
	t.current = nil
	t.mu.Unlock()
	if old != nil {
		// Active requests retain their own deadlines; the retired pool cannot
		// accept new requests, even if an old HTTP/2 stream is still active.
		old.CloseIdleConnections()
	}
}

func newClient(c config) *client {
	return &client{cfg: c, http: &http.Client{
		Transport: tfsConnections,
		Timeout:   tfsRequestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

func retryableTFSRead(method, path string) bool {
	// WIQL uses POST but only reads data. Never retry creation, comments,
	// votes, edits or merges: a lost response does not imply a failed write.
	return method == http.MethodGet || method == http.MethodHead ||
		(method == http.MethodPost && path == "wit/wiql")
}

func networkFailure(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	// Certificate errors require fixing trust/hostname, not reconnecting.
	var certificateError *tls.CertificateVerificationError
	if errors.As(err, &certificateError) {
		return false
	}
	var networkError net.Error
	return errors.As(err, &networkError) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

func (c *client) request(ctx context.Context, method, path string, q url.Values, body any, out any) (err error) {
	if timings, ok := ctx.Value(timingKey{}).(*requestTimings); ok {
		start := time.Now()
		defer func() { timings.record(start, err) }()
	}
	budget := c.http.Timeout
	if budget <= 0 {
		budget = tfsRequestTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	safe := retryableTFSRead(method, path)
	for attempt := 0; ; attempt++ {
		attemptCtx := ctx
		stop := func() {}
		if safe && attempt == 0 {
			// Reserve time for recovery rather than spending the whole budget
			// waiting on an unreachable connection from the previous VPN.
			attemptCtx, stop = context.WithTimeout(ctx, budget/2)
		}
		err := c.requestOnce(attemptCtx, method, path, q, body, out)
		stop()
		if !networkFailure(err) {
			return err
		}
		c.http.CloseIdleConnections()
		if !safe {
			return fmt.Errorf("%w. Запрос изменения мог выполниться: проверьте результат в TFS перед повтором", err)
		}
		if attempt == 1 || ctx.Err() != nil {
			return err
		}
		timer := time.NewTimer(tfsRetryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("TFS недоступен: %w", ctx.Err())
		case <-timer.C:
		}
	}
}
