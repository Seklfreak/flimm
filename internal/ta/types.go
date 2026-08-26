// Package ta is the TubeArchivist API client: a Client interface the handlers
// depend on, the HTTP implementation with a small TTL cache, and a Fake for
// tests. Field names mirror the TA index documents.
package ta

import (
	"encoding/json"
	"strings"
	"time"
)

// Video is a TA video document (list and detail share the shape).
type Video struct {
	YoutubeID      string       `json:"youtube_id"`
	Title          string       `json:"title"`
	Description    string       `json:"description"`
	Channel        Channel      `json:"channel"`
	VidThumbURL    string       `json:"vid_thumb_url"`
	MediaURL       string       `json:"media_url"`
	Published      string       `json:"published"` // "2006-01-02"
	DateDownloaded int64        `json:"date_downloaded"`
	VidType        string       `json:"vid_type"` // videos|shorts|streams
	Player         Player       `json:"player"`
	Subtitles      []Subtitle   `json:"subtitles"`
	Sponsorblock   Sponsorblock `json:"sponsorblock"`
	Stats          Stats        `json:"stats"`
	Tags           []string     `json:"tags"`
	Playlist       []string     `json:"playlist"`
	Streams        []Stream     `json:"streams"`
	Active         bool         `json:"active"`
}

type Player struct {
	Duration    float64 `json:"duration"`
	DurationStr string  `json:"duration_str"`
	Watched     bool    `json:"watched"`
	Progress    float64 `json:"progress"`
}

type Subtitle struct {
	Lang     string `json:"lang"`
	Source   string `json:"source"` // user|auto
	MediaURL string `json:"media_url"`
	Name     string `json:"name"`
}

type Sponsorblock struct {
	Segments  []SponsorSegment `json:"segments"`
	IsEnabled bool             `json:"is_enabled"`
}

type SponsorSegment struct {
	Category string     `json:"category"`
	Segment  [2]float64 `json:"segment"`
}

type Stats struct {
	ViewCount int64 `json:"view_count"`
	LikeCount int64 `json:"like_count"`
}

type Stream struct {
	Type    string `json:"type"`
	Codec   string `json:"codec"`
	Height  int    `json:"height"`
	Width   int    `json:"width"`
	Bitrate int    `json:"bitrate"`
}

// Channel is a TA channel document (also embedded in videos).
type Channel struct {
	ChannelID          string `json:"channel_id"`
	ChannelName        string `json:"channel_name"`
	ChannelThumbURL    string `json:"channel_thumb_url"`
	ChannelBannerURL   string `json:"channel_banner_url"`
	ChannelDescription string `json:"channel_description"`
	ChannelSubscribed  bool   `json:"channel_subscribed"`
	ChannelActive      bool   `json:"channel_active"`
	ChannelLastRefresh string `json:"channel_last_refresh"`
}

// Playlist is a TA playlist document; Entries carry the ordered video ids.
type Playlist struct {
	PlaylistID          string          `json:"playlist_id"`
	PlaylistName        string          `json:"playlist_name"`
	PlaylistChannel     string          `json:"playlist_channel"`
	PlaylistChannelID   string          `json:"playlist_channel_id"`
	PlaylistThumbnail   string          `json:"playlist_thumbnail"`
	PlaylistType        string          `json:"playlist_type"` // regular|custom
	PlaylistDescription string          `json:"playlist_description"`
	PlaylistEntries     []PlaylistEntry `json:"playlist_entries"`
	PlaylistActive      bool            `json:"playlist_active"`
}

type PlaylistEntry struct {
	YoutubeID  string `json:"youtube_id"`
	Title      string `json:"title"`
	Uploader   string `json:"uploader"`
	Idx        int    `json:"idx"`
	Downloaded bool   `json:"downloaded"`
}

// Paginate is TA's list pagination block.
type Paginate struct {
	PageSize    int `json:"page_size"`
	PageFrom    int `json:"page_from"`
	CurrentPage int `json:"current_page"`
	LastPage    int `json:"last_page"`
	TotalHits   int `json:"total_hits"`
}

// VideoPage is one page of a TA video list.
type VideoPage struct {
	Data     []Video
	Paginate Paginate
}

// VideoQuery mirrors the TA /api/video/ filters.
type VideoQuery struct {
	Channel  string
	Playlist string
	Watch    string // watched|unwatched|continue|""
	Sort     string // published|downloaded|views|likes|duration|filesize
	Order    string // asc|desc
	Type     string // videos|shorts|streams|""
	Page     int    // 1-based
	PageSize int
}

// ChannelStats is derived from a one-item video list per channel: how many
// videos TA holds and when the newest was published.
type ChannelStats struct {
	VideoCount int
	LastUpload time.Time
}

// SearchResult is TA's /api/search/ response, one bucket per index.
type SearchResult struct {
	Videos    []Video
	Channels  []Channel
	Playlists []Playlist
	Fulltext  []SubtitleHit
}

// SubtitleHit is one fulltext (ta_subtitle) match.
type SubtitleHit struct {
	YoutubeID     string  `json:"youtube_id"`
	Title         string  `json:"title"`
	SubtitleLine  string  `json:"subtitle_line"`
	SubtitleStart float64 `json:"subtitle_start"`
	SubtitleEnd   float64 `json:"subtitle_end"`
	SubtitleLang  string  `json:"subtitle_lang"`
	ChannelID     string  `json:"subtitle_channel_id"`
	Channel       string  `json:"subtitle_channel"`
}

// Comments is the TA comment tree, passed through untouched.
type Comments = json.RawMessage

// PublishedTime parses the TA published date (day precision).
func (v Video) PublishedTime() time.Time {
	t, _ := parseTADate(v.Published)
	return t
}

// DownloadedTime converts date_downloaded (unix seconds).
func (v Video) DownloadedTime() time.Time {
	if v.DateDownloaded == 0 {
		return time.Time{}
	}
	return time.Unix(v.DateDownloaded, 0).UTC()
}

// Kind maps TA's vid_type to the API's video|short|stream.
func (v Video) Kind() string {
	switch v.VidType {
	case "shorts":
		return "short"
	case "streams":
		return "stream"
	default:
		return "video"
	}
}

// Height is the video stream height (0 if unknown).
func (v Video) Height() int {
	for _, s := range v.Streams {
		if s.Type == "video" && s.Height > 0 {
			return s.Height
		}
	}
	return 0
}

func parseTADate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{"2006-01-02", time.RFC3339, "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, errBadDate
}
