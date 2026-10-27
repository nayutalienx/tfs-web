package main

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

var attachmentUUID = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)

// Trust only attachment IDs from the configured collection, never arbitrary URLs.
func attachmentID(base, source string) string {
	b, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil {
		return ""
	}
	u, err := url.Parse(source)
	if err != nil || u.User != nil || u.Fragment != "" {
		return ""
	}
	u = b.ResolveReference(u)
	if u.Scheme != b.Scheme || !strings.EqualFold(u.Host, b.Host) {
		return ""
	}
	prefix := strings.TrimRight(b.Path, "/") + "/"
	if !strings.HasPrefix(u.Path, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(u.Path, prefix)
	parts := strings.Split(rest, "/")
	// TFS returns either collection-scoped or project-scoped attachment URLs.
	if len(parts) == 5 {
		if parts[0] == "" || parts[0] == "." || parts[0] == ".." {
			return ""
		}
		parts = parts[1:]
	}
	if len(parts) != 4 || parts[0] != "_apis" || parts[1] != "wit" || parts[2] != "attachments" || !attachmentUUID.MatchString(parts[3]) {
		return ""
	}
	return parts[3]
}

type imageContent struct {
	Data []byte
	Type string
}

func (image *imageContent) read(r io.Reader) error {
	data, err := io.ReadAll(io.LimitReader(r, (10<<20)+1))
	if err != nil {
		return err
	}
	if len(data) > 10<<20 {
		return fmt.Errorf("Скриншот больше 10 МиБ")
	}
	kind := http.DetectContentType(data)
	switch kind {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
	default:
		return fmt.Errorf("Вложение не является поддерживаемым растровым изображением")
	}
	image.Data, image.Type = data, kind
	return nil
}
func (s *server) screenshot(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !attachmentUUID.MatchString(id) {
		http.Error(w, "Некорректный ID вложения", 400)
		return
	}
	var image imageContent
	if err := s.c.request(r.Context(), "GET", "wit/attachments/"+id, url.Values{"download": {"false"}}, nil, &image); err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", image.Type)
	w.Header().Set("Content-Disposition", `inline; filename="screenshot"`)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Write(image.Data)
}
