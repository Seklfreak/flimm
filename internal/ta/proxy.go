package ta

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"
	"time"
)

// mediaContentTypes maps a media file extension to the Content-Type the
// browser needs. TubeArchivist's nginx declares a `types { text/vtt vtt; }`
// block on /media/, which REPLACES the default MIME map for that location, so
// every .mp4 comes back as application/octet-stream — and <video> refuses to
// decode that. We restore the type from the extension.
var mediaContentTypes = map[string]string{
	".mp4":  "video/mp4",
	".m4v":  "video/mp4",
	".webm": "video/webm",
	".mkv":  "video/x-matroska",
	".ogv":  "video/ogg",
	".m4a":  "audio/mp4",
	".mp3":  "audio/mpeg",
	".opus": "audio/opus",
	".vtt":  "text/vtt",
	".srt":  "text/plain; charset=utf-8",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".webp": "image/webp",
}

// fixMediaHeaders sets Content-Type from the requested file extension when the
// upstream type is missing or the generic octet-stream, and advertises range
// support so players can seek.
func fixMediaHeaders(resp *http.Response) error {
	ext := strings.ToLower(path.Ext(resp.Request.URL.Path))
	if want, ok := mediaContentTypes[ext]; ok {
		if got := resp.Header.Get("Content-Type"); got == "" || strings.HasPrefix(got, "application/octet-stream") {
			resp.Header.Set("Content-Type", want)
		}
	}
	if resp.Header.Get("Accept-Ranges") == "" && (resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusPartialContent) {
		resp.Header.Set("Accept-Ranges", "bytes")
	}
	return nil
}

// NewMediaProxy returns a reverse proxy to TA's nginx for /media/* and
// /cache/* assets. The request's URL path is forwarded as-is (the caller
// rewrites it to the TA path first) with the Token header added and browser
// credentials stripped. Range / If-Range go upstream untouched and 206 /
// Content-Range / Accept-Ranges / Content-Length / Content-Type come back
// unchanged; the body is streamed, never buffered.
func NewMediaProxy(baseURL, token string) (*httputil.ReverseProxy, error) {
	target, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 30 * time.Second
	return &httputil.ReverseProxy{
		Transport:      transport,
		ModifyResponse: fixMediaHeaders,
		// -1 flushes every write straight through: a byte-range read of a
		// large mp4 must not sit in a buffer.
		FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = target.Host
			pr.Out.Header.Set("Authorization", "Token "+token)
			pr.Out.Header.Del("Cookie")
		},
	}, nil
}
