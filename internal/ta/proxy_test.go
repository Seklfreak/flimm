package ta

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFixMediaHeaders(t *testing.T) {
	cases := []struct {
		name, path, upstream, want string
	}{
		// TA's nginx types block leaves mp4 as octet-stream; <video> needs the real type.
		{"mp4 octet-stream", "/media/UC/vid.mp4", "application/octet-stream", "video/mp4"},
		{"mp4 empty", "/media/UC/vid.mp4", "", "video/mp4"},
		{"vtt kept", "/media/UC/vid.en.vtt", "text/vtt", "text/vtt"},
		{"jpeg kept", "/cache/videos/a/abc.jpg", "image/jpeg", "image/jpeg"},
		{"unknown ext untouched", "/media/UC/vid.bin", "application/octet-stream", "application/octet-stream"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://ta"+c.path, nil)
			resp := &http.Response{StatusCode: http.StatusPartialContent, Header: http.Header{}, Request: req}
			if c.upstream != "" {
				resp.Header.Set("Content-Type", c.upstream)
			}
			if err := fixMediaHeaders(resp); err != nil {
				t.Fatal(err)
			}
			if got := resp.Header.Get("Content-Type"); got != c.want {
				t.Errorf("Content-Type = %q, want %q", got, c.want)
			}
			if got := resp.Header.Get("Accept-Ranges"); got != "bytes" {
				t.Errorf("Accept-Ranges = %q, want bytes", got)
			}
		})
	}
}

func TestFixMediaHeadersLeavesErrorsAlone(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://ta/media/UC/vid.mp4", nil)
	resp := &http.Response{StatusCode: http.StatusNotFound, Header: http.Header{}, Request: req}
	if err := fixMediaHeaders(resp); err != nil {
		t.Fatal(err)
	}
	if got := resp.Header.Get("Accept-Ranges"); got != "" {
		t.Errorf("Accept-Ranges on 404 = %q, want empty", got)
	}
}
