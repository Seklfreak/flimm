package ta

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Layout is where a TubeArchivist release serves artwork from.
//
// Videos and subtitles need no layout: their documents carry a media_url and
// taMediaPath maps it either way. Artwork is the part Flimm addresses by id,
// so it has to know which scheme the archive speaks. The zero value is the
// current one, which is what an archive whose version cannot be read is
// assumed to run.
type Layout int

const (
	// LayoutApp is v0.6 and later: TubeArchivist serves media itself, under
	// /media/thumb/….
	LayoutApp Layout = iota
	// LayoutNginx is v0.5 and earlier: the bundled nginx serves the thumbnail
	// cache under /cache/…, videos sharded by the first character of the id.
	LayoutNginx
)

// layoutTTL is how long a version read off /api/ping/ is trusted. Short,
// because an archive upgraded underneath a running Flimm would otherwise
// lose every picture until a restart.
const layoutTTL = time.Minute

// LayoutForVersion maps TubeArchivist's reported version ("v0.5.12",
// "v0.6.0", "unstable", …) to a layout. Anything that does not parse as a
// release older than v0.6 is taken to be the current layout.
func LayoutForVersion(version string) Layout {
	v := strings.TrimPrefix(strings.TrimSpace(version), "v")
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return LayoutApp
	}
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return LayoutApp
	}
	if major == 0 && minor < 6 {
		return LayoutNginx
	}
	return LayoutApp
}

// VideoThumb is the archive path of a video's thumbnail.
func (l Layout) VideoThumb(id string) string {
	if l == LayoutNginx {
		return "/cache/videos/" + strings.ToLower(id[:1]) + "/" + id + ".jpg"
	}
	return "/media/thumb/video/" + id + ".jpg"
}

// ChannelArt is the archive path of a channel's artwork; kind is "thumb",
// "banner" or "tvart".
func (l Layout) ChannelArt(id, kind string) string {
	if l == LayoutNginx {
		return "/cache/channels/" + id + "_" + kind + ".jpg"
	}
	return "/media/thumb/channel/" + id + "/" + kind + ".jpg"
}

// PlaylistThumb is the archive path of a playlist's own thumbnail.
func (l Layout) PlaylistThumb(id string) string {
	if l == LayoutNginx {
		return "/cache/playlists/" + id + ".jpg"
	}
	return "/media/thumb/playlist/" + id + ".jpg"
}

type pingResponse struct {
	Version string `json:"version"`
}

// MediaLayout reports which layout the archive serves, read off /api/ping/
// and cached for layoutTTL. When the ping fails the last known layout stands
// (the current one if there is none yet): the request that asked is about to
// fail against an unreachable archive anyway, and a blip must not flip it.
func (c *HTTP) MediaLayout(ctx context.Context) Layout {
	c.mu.Lock()
	if c.layout != nil && time.Now().Before(c.layout.exp) {
		l := c.layout.val
		c.mu.Unlock()
		return l
	}
	c.mu.Unlock()

	var p pingResponse
	err := c.do(ctx, http.MethodGet, "/api/ping/", nil, nil, &p)

	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		if c.layout != nil {
			return c.layout.val
		}
		return LayoutApp
	}
	c.layout = &cached[Layout]{val: LayoutForVersion(p.Version), exp: time.Now().Add(layoutTTL)}
	return c.layout.val
}
