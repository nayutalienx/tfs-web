package main

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
)

const screenshotID = "00000000-0000-4000-8000-000000000001"

func TestScreenshotAllowlist(t *testing.T) {
	base := "https://tfs.example/DefaultCollection"
	good := base + "/project-id/_apis/wit/attachments/" + screenshotID + "?fileName=image.png"
	if attachmentID(base, good) != screenshotID {
		t.Fatal("project scoped image rejected")
	}
	output := string(safeRich(`<p>Снимок</p><img src="`+good+`" onerror="alert(1)" style="width:99999px">`, base))
	if !strings.Contains(output, `src="/attachments/`+screenshotID+`"`) || strings.Contains(output, "onerror") || strings.Contains(output, "style=") {
		t.Fatal(output)
	}
	for _, bad := range []string{"https://evil.example/_apis/wit/attachments/" + screenshotID, base + "-evil/_apis/wit/attachments/" + screenshotID, base + "/../_apis/wit/attachments/" + screenshotID, "data:image/svg+xml,test", "javascript:alert(1)", base + "/_apis/wit/attachments/not-a-guid", "https://user@tfs.example/DefaultCollection/_apis/wit/attachments/" + screenshotID} {
		if attachmentID(base, bad) != "" {
			t.Errorf("accepted %s", bad)
		}
		if strings.Contains(string(safeRich(`<img src="`+bad+`">`, base)), "<img") {
			t.Errorf("rendered %s", bad)
		}
	}
}

func TestScreenshotProxy(t *testing.T) {
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 40)...)
	for _, bad := range []bool{false, true} {
		t.Run(map[bool]string{false: "png", true: "svg"}[bad], func(t *testing.T) {
			calls := 0
			app := testApp(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/DemoProject/_apis/wit/attachments/"+screenshotID {
					t.Errorf("bad upstream path %s", r.URL.Path)
				}
				_, pat, _ := r.BasicAuth()
				if pat != "test-secret" {
					t.Error("missing authentication")
				}
				if bad {
					w.Write([]byte(`<svg onload="alert(1)"></svg>`))
				} else {
					w.Write(png)
				}
			})
			response := appRequest(app, "GET", "/attachments/"+screenshotID, nil)
			if bad {
				if response.Code != 502 {
					t.Fatalf("svg status %d", response.Code)
				}
			} else {
				if response.Code != 200 || response.Header().Get("Content-Type") != "image/png" || !bytes.Equal(response.Body.Bytes(), png) {
					t.Fatalf("bad png response: %d", response.Code)
				}
				if response.Header().Get("X-Content-Type-Options") != "nosniff" {
					t.Error("missing nosniff")
				}
			}
			appRequest(app, "GET", "/attachments/bad-id", nil)
			if calls != 1 {
				t.Fatalf("invalid id reached upstream")
			}
		})
	}
}

func TestScreenshotSizeLimit(t *testing.T) {
	var image imageContent
	if image.read(bytes.NewReader(make([]byte, (10<<20)+1))) == nil {
		t.Fatal("oversized attachment accepted")
	}
}

func TestNativeScriptServedLocally(t *testing.T) {
	app := testApp(t, func(w http.ResponseWriter, r *http.Request) { t.Fatal("static asset must not call TFS") })
	response := appRequest(app, "GET", "/static/ui.js", nil)
	if response.Code != 200 || !strings.HasPrefix(response.Header().Get("Content-Type"), "text/javascript") || !bytes.Equal(response.Body.Bytes(), uiScript) {
		t.Fatal("native script not served correctly")
	}
	csp := response.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self';") || strings.Contains(csp, "unsafe-eval") || strings.Contains(csp, "script-src 'unsafe-inline'") {
		t.Fatalf("unsafe script policy: %s", csp)
	}
}
