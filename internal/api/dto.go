package api

import (
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/Seklfreak/flimm/internal/db/sqlc"
	"github.com/Seklfreak/flimm/internal/sponsorblock"
	"github.com/Seklfreak/flimm/internal/ta"
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
	// Dismissed is "taken out of the feeds without watching it". Feed
	// listings drop these; a channel page, search and playlists still show
	// them, which is where a viewer puts one back.
	Dismissed    bool       `json:"dismissed"`
	Position     float64    `json:"position"`
	Progress     float64    `json:"progress"`
	LastPlayedAt *time.Time `json:"last_played_at"`
}

type SubtitleTrack struct {
	Lang   string `json:"lang"`
	Source string `json:"source"`
	URL    string `json:"url"`
}

// SponsorSegment is one crowd-sourced SponsorBlock range. ActionType says
// what a player may do with it: only `skip` may be skipped automatically,
// `mute` is muted for its length, `poi` is a single point of interest (the
// highlight, where Start == End) and `full` labels the whole video rather
// than a range of it. It is `skip` for the segments TubeArchivist indexed at
// download time, which carry no action of their own.
type SponsorSegment struct {
	Category   string  `json:"category"`
	ActionType string  `json:"action_type"`
	Start      float64 `json:"start"`
	End        float64 `json:"end"`
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

// StreamInfo describes one source rendition TA muxed the video from. Native
// clients use Codec to decide whether MediaURL is directly playable by
// AVFoundation (H.264/AAC always is; VP9/AV1/Opus support is device-dependent).
type StreamInfo struct {
	Type    string `json:"type"` // video|audio
	Codec   string `json:"codec"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	Bitrate int    `json:"bitrate"`
}

// HLSVariantInfo is one offered quality of the compatible video rendition.
// Codec is `h264` up to 1080p and `hevc` above it (which every Apple device
// since the iPhone 7 and the Apple TV 4K decodes in hardware); a client that
// cannot decode HEVC picks a height of 1080 or below.
type HLSVariantInfo struct {
	Height int    `json:"height"`
	URL    string `json:"url"`
	State  string `json:"state"` // pending|running|done|failed
	Codec  string `json:"codec"` // h264|hevc
	// Progress is the fraction of the rendition's segments that exist, 0..1.
	// It is not "how far playback can get": the playlist is complete from the
	// first request and segments are filled in wherever the viewer is, so this
	// is only how much of the whole video has been transcoded. 1 for a
	// finished rendition, 0 for one nothing has asked for.
	Progress float64 `json:"hls_progress"`
}

// HLSStartResponse is what POST /videos/{id}/hls answers with: which rendition
// was started or re-aimed, and where it stands.
type HLSStartResponse struct {
	State    string  `json:"state"` // pending|running|done|failed
	Height   int     `json:"height"`
	Progress float64 `json:"hls_progress"`
}

type VideoDetail struct {
	VideoSummary
	Description string `json:"description"`
	Height      int    `json:"height"`
	MediaURL    string `json:"media_url"`
	// AudioURL is the derived audio-only rendition; see internal/media.
	AudioURL string `json:"audio_url"`
	// AudioAACURL is the same audio as AAC in MP4, for players that cannot
	// decode Opus in WebM (AVFoundation); a re-encode unless the source is
	// already AAC.
	AudioAACURL string `json:"audio_aac_url"`
	// HLSURL is the default compatible video rendition (1080p, or the tallest
	// the source can fill when it is smaller), derived on first request.
	// Always present — HLSState says whether it is ready. Clients that pick a
	// quality use HLSVariants instead.
	HLSURL string `json:"hls_url"`
	// PreviewURL is the WebVTT track of scrub-preview stills — the picture a
	// player shows above the scrubber while it is dragged. Always present:
	// asking for it is what starts deriving it, and a 404 means "not yet",
	// which a player answers by scrubbing without pictures and asking again.
	PreviewURL string `json:"preview_url"`
	// HLSState is pending|running|done|failed for that rendition. Pending
	// means nobody has asked for it yet.
	HLSState string `json:"hls_state"`
	// HLSVariants is every quality this video offers, tallest first. Each is
	// derived independently on first request, so their states differ.
	HLSVariants  []HLSVariantInfo   `json:"hls_variants"`
	YoutubeURL   string             `json:"youtube_url"`
	Streams      []StreamInfo       `json:"streams"`
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
	// Pinned and Music are Flimm's own per-user state — TubeArchivist has no
	// concept of either. Music means the playlist is played as audio and
	// carries no watch state: songs are replayed, so "seen" is meaningless.
	Pinned bool `json:"pinned"`
	Music  bool `json:"music"`
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

// Comment is one archived comment, as `GET /videos/{id}/comments` returns it.
//
// Normalised from what TubeArchivist indexed, so no client parses upstream's
// key names. There is deliberately **no author avatar**: the archive holds a
// Google CDN URL for it, and a client loading that would announce every video
// its viewer opens to a third party — which is the one thing showing archived
// comments otherwise avoids entirely.
type Comment struct {
	ID       string `json:"id"`
	Author   string `json:"author"`
	AuthorID string `json:"author_id"`
	Text     string `json:"text"`
	Likes    int    `json:"likes"`
	// Published is when it was written, when the archive recorded that.
	Published *time.Time `json:"published"`
	// TimeText is upstream's own relative wording ("2 days ago"), kept for
	// archives that carry it and no timestamp. Clients prefer Published.
	TimeText string `json:"time_text"`
	// Hearted by the uploader.
	Hearted bool `json:"hearted"`
	// FromUploader marks a comment by the channel that published the video.
	FromUploader bool `json:"from_uploader"`
	// Replies to this comment, in the order the archive holds them. A thread
	// travels with its parent rather than being paged on its own.
	Replies []Comment `json:"replies"`
}

// LoudnessInfo is `GET /videos/{id}/loudness`: how loud a video was measured
// to be, and the gain a player should apply to it.
type LoudnessInfo struct {
	// State is pending|running|done|failed for the measurement pass. Only
	// `done` carries numbers; the others carry a GainDB of 0, which is the
	// honest thing to apply when nothing is known.
	State string `json:"state"`
	// GainDB is what to apply, in decibels — the whole point of the endpoint.
	// Never positive: see the note in internal/media/loudness.go.
	GainDB float64 `json:"gain_db"`
	// TargetLUFS is the programme loudness that gain aims at. Always present,
	// so a client can say what it is doing without hardcoding it.
	TargetLUFS float64 `json:"target_lufs"`
	// MeasuredLUFS, PeakDBTP and RangeLU are the measurement itself. Nothing
	// has to read them — the gain is derived from them here — but a client
	// showing "this video is 6 dB loud" or a person debugging a strange one
	// needs them.
	MeasuredLUFS float64 `json:"measured_lufs"`
	PeakDBTP     float64 `json:"peak_dbtp"`
	RangeLU      float64 `json:"range_lu"`
}

// ---- prefs ----

type Prefs struct {
	Autoplay      bool    `json:"autoplay"`
	PlaybackSpeed float64 `json:"playback_speed"`
	// SubtitleLang is a language code, or "off" when the viewer turned
	// subtitles off. Defaults to English so archived CC plays by default.
	SubtitleLang string `json:"subtitle_lang"`
	SubtitleSize string `json:"subtitle_size"`
	// SkipSponsors is the master switch: false and no SponsorBlock segment
	// acts at all, whatever SponsorActions says.
	SkipSponsors bool `json:"skip_sponsors"`
	// SponsorActions is what each category does while SkipSponsors is on:
	// "skip" (seek past it), "ask" (offer the viewer a button) or "off"
	// (tint the scrubber and nothing else). A category the map does not
	// mention takes its default; the categories that mark an instant rather
	// than a range — the highlight — are not in it at all, because a point of
	// interest is only ever offered.
	SponsorActions map[string]string `json:"sponsor_actions"`
	// DeArrowTitles and DeArrowThumbnails are set independently, because they
	// are independent things to want: a viewer may trust the crowd's words and
	// not its frames, or the other way round. Each is "off", "manual" (only
	// what a person submitted and the crowd voted on) or "all" (that, and what
	// DeArrow generates when nobody submitted anything).
	DeArrowTitles     string `json:"dearrow_titles"`
	DeArrowThumbnails string `json:"dearrow_thumbnails"`
	// NormalizeLoudness evens out the difference between channels: the player
	// applies the gain from `GET /videos/{id}/loudness` instead of playing
	// every video at whatever level it was uploaded at.
	NormalizeLoudness       bool   `json:"normalize_loudness"`
	EverythingSort          string `json:"everything_sort"`
	EverythingHideSeen      bool   `json:"everything_hide_seen"`
	EverythingIncludeShorts bool   `json:"everything_include_shorts"`
	Theme                   string `json:"theme"`
}

func defaultPrefs() Prefs {
	return Prefs{
		Autoplay:       true,
		PlaybackSpeed:  1.0,
		SubtitleLang:   defaultSubtitleLang,
		SubtitleSize:   "medium",
		SkipSponsors:   true,
		SponsorActions: defaultSponsorActions(),
		// Off by default, both. DeArrow rewrites what every video is called
		// and what it looks like — a strong opinion to hold on someone's
		// behalf — and it involves asking a third party (by hash prefix)
		// about the videos being browsed. It is opted into.
		DeArrowTitles:     dearrowOff,
		DeArrowThumbnails: dearrowOff,
		// On, unlike DeArrow: this asks nobody anything, changes nothing about
		// what a video *is*, and undoes a real daily annoyance — reaching for
		// the volume between one channel and the next. It only ever turns a
		// video down, and one switch turns it off.
		NormalizeLoudness:  true,
		EverythingSort:     "newest",
		EverythingHideSeen: true,
		Theme:              "system",
	}
}

// What a category does when the viewer has never said.
//
// The three that interrupt a video without being part of it are skipped, which
// is what Flimm has always done. The rest are offered rather than taken: an
// intro or a recap is sometimes exactly what someone wants to watch, so the
// viewer gets a button instead of a jump they did not ask for.
func defaultSponsorActions() map[string]string {
	return map[string]string{
		sponsorblock.CategorySponsor:     sponsorSkip,
		sponsorblock.CategorySelfPromo:   sponsorSkip,
		sponsorblock.CategoryInteraction: sponsorSkip,
		sponsorblock.CategoryIntro:       sponsorAsk,
		sponsorblock.CategoryOutro:       sponsorAsk,
		sponsorblock.CategoryPreview:     sponsorAsk,
		sponsorblock.CategoryMusicOff:    sponsorAsk,
		sponsorblock.CategoryFiller:      sponsorAsk,
		sponsorblock.CategoryExclusive:   sponsorAsk,
	}
}

// What a category can be set to.
const (
	sponsorSkip = "skip"
	sponsorAsk  = "ask"
	sponsorOff  = "off"
)

var validSponsorActions = map[string]bool{sponsorSkip: true, sponsorAsk: true, sponsorOff: true}

// What a DeArrow preference can be.
const (
	// dearrowOff leaves the archive's own title and thumbnail alone.
	dearrowOff = "off"
	// dearrowManual uses only what a person submitted and the crowd voted on.
	dearrowManual = "manual"
	// dearrowAll adds what DeArrow generates when nobody has submitted: a
	// title tidied of shouting, and a frame the service picked itself.
	dearrowAll = "all"
)

var validDeArrow = map[string]bool{dearrowOff: true, dearrowManual: true, dearrowAll: true}

var (
	validSorts         = map[string]bool{"newest": true, "oldest": true, "shortest": true, "longest": true}
	validSubtitleSizes = map[string]bool{"small": true, "medium": true, "large": true}
	validThemes        = map[string]bool{"system": true, "light": true, "dark": true}
	prefKeys           = map[string]bool{
		"autoplay": true, "playback_speed": true, "subtitle_lang": true, "subtitle_size": true,
		"skip_sponsors": true, "sponsor_actions": true,
		"dearrow_titles": true, "dearrow_thumbnails": true,
		"normalize_loudness": true,
		"everything_sort":    true, "everything_hide_seen": true,
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
	if !validDeArrow[p.DeArrowTitles] {
		return fmt.Errorf("invalid dearrow_titles")
	}
	if !validDeArrow[p.DeArrowThumbnails] {
		return fmt.Errorf("invalid dearrow_thumbnails")
	}
	for category, action := range p.SponsorActions {
		if !validSponsorActions[action] {
			return fmt.Errorf("invalid sponsor_actions[%q]: %q", category, action)
		}
		if _, ok := defaultSponsorActions()[category]; !ok {
			return fmt.Errorf("sponsor_actions has no category %q", category)
		}
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
	// A stored map replaces the default one wholesale, so a category added to
	// Flimm after the row was written would come back missing rather than at
	// its default. Fill those in; a category the viewer *did* set is theirs.
	if p.SponsorActions == nil {
		p.SponsorActions = map[string]string{}
	}
	for category, action := range defaultSponsorActions() {
		if _, ok := p.SponsorActions[category]; !ok {
			p.SponsorActions[category] = action
		}
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
		if !validDeArrow[p.DeArrowTitles] {
			p.DeArrowTitles = d.DeArrowTitles
		}
		if !validDeArrow[p.DeArrowThumbnails] {
			p.DeArrowThumbnails = d.DeArrowThumbnails
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

// resumeRewind is how far back playback picks up from where it was left.
// Dropping straight into the middle of a sentence costs a viewer more than
// fifteen seconds does, so the position *reported* to a client is that much
// earlier than the one stored.
//
// It lives here, where the resume point is composed, rather than in four
// players: the position written back (and on to TubeArchivist) is never
// moved, and `progress` is computed before the rewind, so a card's bar still
// shows how far the viewer actually got.
const resumeRewind = 15

// resumeFrom is where a client should start, given where playback stopped.
func resumeFrom(position float64) float64 {
	return max(0, position-resumeRewind)
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
		// A watched video is not resumed — every client starts it over — so
		// there is nothing to rewind, and moving it would only misreport
		// where the viewer got to.
		return out
	}
	out.Position = resumeFrom(out.Position)
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
	if !out.Watched {
		out.Position = resumeFrom(out.Position)
	}
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
