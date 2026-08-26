package ta

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

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
		Transport: transport,
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
