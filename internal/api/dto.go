package api

import (
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/Seklfreak/archive-client/internal/db/sqlc"
	"github.com/Seklfreak/archive-client/internal/ta"
)

// ---- video ----

type ChannelRef struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ThumbURL string `json:"thumb_url"`
}

type VideoSummary struct {
	ID               string     `json:"id"`
	Title            string     `json:"title"`
	Channel          ChannelRef `json:"channel"`
	ThumbURL         string     `json:"thumb_url"`
	Duration         int        `json:"duration"`
	Published        time.Time  `json:"published"`
	Downloaded       time.Time  `json:"downloaded"`
	Type             string     `json:"type"`
	SubtitleLangs    []string   `json:"subtitle_langs"`
	HasAutoSubtitles bool       `json:"has_auto_subtitles"`
	Watched          bool       `json:"watched"`
	Position         float64    `json:"position"`
	Progress         float64    `json:"progress"`
	LastPlayedAt     *time.Time `json:"last_played_at"`
}

type SubtitleTrack struct {
	Lang   string `json:"lang"`
	Source string `json:"source"`
	URL    string `json:"url"`
}

type SponsorSegment struct {
	Category string  `json:"category"`
	Start    float64 `json:"start"`
	End      float64 `json:"end"`
}

type VideoStats struct {
	Views int64 `json:"views"`
	Likes int64 `json:"likes"`
}

type VideoPlaylistRef struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Position int    `json:"position"`
	Count    int    `json:"count"`
}

type VideoDetail struct {
	VideoSummary
	Description  string             `json:"description"`
	Height       int                `json:"height"`
	MediaURL     string             `json:"media_url"`
	YoutubeURL   string             `json:"youtube_url"`
	Subtitles    []SubtitleTrack    `json:"subtitles"`
	Sponsorblock []SponsorSegment   `json:"sponsorblock"`
	Stats        VideoStats         `json:"stats"`
	Tags         []string           `json:"tags"`
	Playlists    []VideoPlaylistRef `json:"playlists"`
	Channel      ChannelSummary     `json:"channel"`
}

// ---- chapters ----

// Chapter is one scrubber marker. End is the next chapter's start, or the
// video duration for the last one.
// NavResponse positions a video inside the list the player is stepping
// through. Index is -1 when the video isn't in that list (it was opened
// without a context, or has since dropped out of a "hide seen" feed).
type NavResponse struct {
	Index    int           `json:"index"`
	Total    int           `json:"total"`
	Previous *VideoSummary `json:"previous"`
	Next     *VideoSummary `json:"next"`
	// First is the head of the context list, so a client can start a shuffled
	// run without knowing the shuffled order itself.
	First *VideoSummary `json:"first"`
}

type Chapter struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Title string  `json:"title"`
}

// ChaptersResponse is GET /videos/{id}/chapters. Source is
// embedded|description|none; Chapters is never null.
type ChaptersResponse struct {
	Source   string    `json:"source"`
	Chapters []Chapter `json:"chapters"`
}

// ---- channel ----

type FeedRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type ChannelSummary struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	ThumbURL    string     `json:"thumb_url"`
	BannerURL   string     `json:"banner_url"`
	VideoCount  int        `json:"video_count"`
	UnseenCount int        `json:"unseen_count"`
	LastUpload  *time.Time `json:"last_upload"`
	Subscribed  bool       `json:"subscribed"`
	Feeds       []FeedRef  `json:"feeds"`
}

type ChannelDetail struct {
	ChannelSummary
	Description string `json:"description"`
}

// ---- feed ----

const everythingFeedID = "everything"

type FeedDTO struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	ChannelIDs    []string  `json:"channel_ids"`
	ChannelCount  int       `json:"channel_count"`
	UnseenCount   int       `json:"unseen_count"`
	Sort          string    `json:"sort"`
	HideSeen      bool      `json:"hide_seen"`
	IncludeShorts bool      `json:"include_shorts"`
	SubtitlesOnly bool      `json:"subtitles_only"`
	Pinned        bool      `json:"pinned"`
	Position      int       `json:"position"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// ---- playlist ----

type PlaylistChannelRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type PlaylistSummary struct {
	ID              string              `json:"id"`
	Name            string              `json:"name"`
	Kind            string              `json:"kind"`
	Channel         *PlaylistChannelRef `json:"channel"`
	ThumbURL        string              `json:"thumb_url"`
	VideoCount      int                 `json:"video_count"`
	TotalDuration   int                 `json:"total_duration"`
	SeenCount       int                 `json:"seen_count"`
	InProgressCount int                 `json:"in_progress_count"`
	Progress        float64             `json:"progress"`
	ResumeVideoID   *string             `json:"resume_video_id"`
}

type PlaylistItem struct {
	Position int          `json:"position"`
	Video    VideoSummary `json:"video"`
}

type PlaylistDetail struct {
	PlaylistSummary
	Items []PlaylistItem `json:"items"`
}

// ---- history ----

type HistoryEntry struct {
	ID       string       `json:"id"`
	Video    VideoSummary `json:"video"`
	PlayedAt time.Time    `json:"played_at"`
	State    string       `json:"state"`
}

// ---- prefs ----

type Prefs struct {
	Autoplay      bool    `json:"autoplay"`
	PlaybackSpeed float64 `json:"playback_speed"`
	// SubtitleLang is a language code, or "off" when the viewer turned
	// subtitles off. Defaults to English so archived CC plays by default.
	SubtitleLang            string `json:"subtitle_lang"`
	SubtitleSize            string `json:"subtitle_size"`
	SkipSponsors            bool   `json:"skip_sponsors"`
	EverythingSort          string `json:"everything_sort"`
	EverythingHideSeen      bool   `json:"everything_hide_seen"`
	EverythingIncludeShorts bool   `json:"everything_include_shorts"`
	Theme                   string `json:"theme"`
}

func defaultPrefs() Prefs {
	return Prefs{
		Autoplay:           true,
		PlaybackSpeed:      1.0,
		SubtitleLang:       defaultSubtitleLang,
		SubtitleSize:       "medium",
		SkipSponsors:       true,
		EverythingSort:     "newest",
		EverythingHideSeen: true,
		Theme:              "system",
	}
}

var (
	validSorts         = map[string]bool{"newest": true, "oldest": true, "shortest": true, "longest": true}
	validSubtitleSizes = map[string]bool{"small": true, "medium": true, "large": true}
	validThemes        = map[string]bool{"system": true, "light": true, "dark": true}
	prefKeys           = map[string]bool{
		"autoplay": true, "playback_speed": true, "subtitle_lang": true, "subtitle_size": true,
		"skip_sponsors": true, "everything_sort": true, "everything_hide_seen": true,
		"everything_include_shorts": true, "theme": true,
	}
)

// subtitleOff is the explicit "no subtitles" value for SubtitleLang. It has to
// be a distinct value rather than empty/null so that turning subtitles off is
// told apart from a pref that was never set (which gets the default).
const (
	subtitleOff         = "off"
	defaultSubtitleLang = "en"
)

var validSubtitleLang = regexp.MustCompile(`^[A-Za-z]{2,8}(-[A-Za-z0-9]{1,8})*$`)

func (p Prefs) validate() error {
	if p.SubtitleLang != subtitleOff && !validSubtitleLang.MatchString(p.SubtitleLang) {
		return fmt.Errorf("invalid subtitle_lang")
	}
	if p.PlaybackSpeed <= 0 || p.PlaybackSpeed > 4 {
		return fmt.Errorf("playback_speed must be in (0, 4]")
	}
	if !validSubtitleSizes[p.SubtitleSize] {
		return fmt.Errorf("invalid subtitle_size")
	}
	if !validSorts[p.EverythingSort] {
		return fmt.Errorf("invalid everything_sort")
	}
	if !validThemes[p.Theme] {
		return fmt.Errorf("invalid theme")
	}
	return nil
}

// parsePrefs overlays stored JSON on the defaults, so new keys get defaults.
func parsePrefs(raw []byte) Prefs {
	p := defaultPrefs()
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &p)
	}
	// Rows written before subtitles defaulted to English stored null/"" here;
	// treat those as "never chose" so they get the default.
	if p.SubtitleLang == "" {
		p.SubtitleLang = defaultSubtitleLang
	}
	if p.validate() != nil {
		d := defaultPrefs()
		if !validSubtitleSizes[p.SubtitleSize] {
			p.SubtitleSize = d.SubtitleSize
		}
		if !validSorts[p.EverythingSort] {
			p.EverythingSort = d.EverythingSort
		}
		if !validThemes[p.Theme] {
			p.Theme = d.Theme
		}
		if p.PlaybackSpeed <= 0 || p.PlaybackSpeed > 4 {
			p.PlaybackSpeed = d.PlaybackSpeed
		}
		if p.SubtitleLang != subtitleOff && !validSubtitleLang.MatchString(p.SubtitleLang) {
			p.SubtitleLang = d.SubtitleLang
		}
	}
	return p
}

// mergePrefs applies a partial update; unknown keys are rejected.
func mergePrefs(cur Prefs, patch map[string]json.RawMessage) (Prefs, error) {
	for k := range patch {
		if !prefKeys[k] {
			return cur, fmt.Errorf("unknown pref %q", k)
		}
	}
	curJSON, _ := json.Marshal(cur)
	var m map[string]json.RawMessage
	_ = json.Unmarshal(curJSON, &m)
	for k, v := range patch {
		m[k] = v
	}
	merged, _ := json.Marshal(m)
	out := cur
	if err := json.Unmarshal(merged, &out); err != nil {
		return cur, fmt.Errorf("invalid prefs: %w", err)
	}
	if err := out.validate(); err != nil {
		return cur, err
	}
	return out, nil
}

// ---- builders ----

func channelThumbURL(id string) string  { return "/media/thumb/channel/" + id }
func channelBannerURL(id string) string { return "/media/thumb/channel/" + id + "/banner" }
func videoThumbURL(id string) string    { return "/media/thumb/video/" + id }
func playlistThumbURL(id string) string { return "/media/thumb/playlist/" + id }

func channelRef(c ta.Channel) ChannelRef {
	return ChannelRef{ID: c.ChannelID, Name: c.ChannelName, ThumbURL: channelThumbURL(c.ChannelID)}
}

// summarize builds the per-user VideoSummary: watch state from the user's
// watch_event when there is one, else TA's flag / progress.
func summarize(v ta.Video, ev *sqlc.WatchEvent) VideoSummary {
	langs := []string{}
	auto := false
	for _, st := range v.Subtitles {
		if st.Lang == "" {
			continue
		}
		langs = append(langs, st.Lang)
		if st.Source == "auto" {
			auto = true
		}
	}
	out := VideoSummary{
		ID:               v.YoutubeID,
		Title:            v.Title,
		Channel:          channelRef(v.Channel),
		ThumbURL:         videoThumbURL(v.YoutubeID),
		Duration:         int(v.Player.Duration),
		Published:        v.PublishedTime(),
		Downloaded:       v.DownloadedTime(),
		Type:             v.Kind(),
		SubtitleLangs:    langs,
		HasAutoSubtitles: auto,
		Watched:          v.Player.Watched,
		Position:         v.Player.Progress,
	}
	if ev != nil {
		out.Watched = ev.CompletedAt.Valid
		out.Position = ev.Position
		out.LastPlayedAt = tsPtr(ev.LastPlayedAt)
		if out.Duration == 0 {
			out.Duration = int(ev.Duration)
		}
	}
	out.Progress = progressOf(out.Watched, out.Position, out.Duration)
	if out.Watched {
		out.Position = 0
		if ev != nil {
			out.Position = ev.Position
		}
	}
	return out
}

func progressOf(watched bool, position float64, duration int) float64 {
	if watched {
		return 1
	}
	if duration <= 0 || position <= 0 {
		return 0
	}
	return min(position/float64(duration), 1)
}

// summaryFromEvent is the fallback for history entries whose video is gone
// from TA: only the snapshot columns are known.
func summaryFromEvent(ev sqlc.WatchEvent) VideoSummary {
	out := VideoSummary{
		ID:            ev.VideoID,
		Title:         ev.Title,
		Channel:       ChannelRef{ID: ev.ChannelID, Name: ev.ChannelName, ThumbURL: channelThumbURL(ev.ChannelID)},
		ThumbURL:      videoThumbURL(ev.VideoID),
		Duration:      int(ev.Duration),
		Type:          "video",
		SubtitleLangs: []string{},
		Watched:       ev.CompletedAt.Valid,
		Position:      ev.Position,
		LastPlayedAt:  tsPtr(ev.LastPlayedAt),
	}
	out.Progress = progressOf(out.Watched, out.Position, out.Duration)
	return out
}

func feedDTO(f sqlc.Feed, channelIDs []string, unseen int) FeedDTO {
	if channelIDs == nil {
		channelIDs = []string{}
	}
	return FeedDTO{
		ID:            f.ID.String(),
		Name:          f.Name,
		ChannelIDs:    channelIDs,
		ChannelCount:  len(channelIDs),
		UnseenCount:   unseen,
		Sort:          f.Sort,
		HideSeen:      f.HideSeen,
		IncludeShorts: f.IncludeShorts,
		SubtitlesOnly: f.SubtitlesOnly,
		Pinned:        f.Pinned,
		Position:      int(f.Position),
		CreatedAt:     ts(f.CreatedAt),
		UpdatedAt:     ts(f.UpdatedAt),
	}
}
