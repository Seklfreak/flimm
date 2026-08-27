// Package faketa is a stand-in TubeArchivist for local development: the
// subset of TA's API that Flimm actually calls, a small deterministic
// catalogue, and generated media files that really play.
//
// It exists so the whole product can be run — and the native apps walked
// through in a simulator — without a TubeArchivist instance, and without
// writing watch state back into a real archive.
package faketa

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/Seklfreak/flimm/internal/ta"
)

// Catalogue is the whole fake archive: channels, videos and playlists, plus
// the per-video watch state the API mutates.
type Catalogue struct {
	Channels  []ta.Channel
	Videos    []ta.Video
	Playlists []ta.Playlist
}

// spec describes one video before its media file exists. Duration is what the
// generator actually encodes, so the document and the file agree.
type spec struct {
	title    string
	seconds  float64
	kind     string // videos|shorts|streams
	codec    string // avc1|vp09 — vp09 is what makes a client reach for the HLS rendition
	height   int
	chapters bool
	sponsors bool
}

// The catalogue is deliberately small and deliberately varied: enough shapes
// that every screen has something to show, and every playback path something
// to exercise.
var channelSpecs = []struct {
	id     string
	name   string
	videos []spec
}{
	{
		id:   "UC-fake-workshop",
		name: "The Workshop",
		videos: []spec{
			{title: "Building a dovetail jig", seconds: 90, kind: "videos", codec: "avc1", height: 1080, chapters: true, sponsors: true},
			{title: "Sharpening, properly", seconds: 60, kind: "videos", codec: "avc1", height: 720, chapters: true},
			{title: "Shop tour 2026", seconds: 45, kind: "videos", codec: "avc1", height: 1080, sponsors: true},
			{title: "One-minute finish test", seconds: 30, kind: "shorts", codec: "avc1", height: 720},
		},
	},
	{
		id:   "UC-fake-kitchen",
		name: "Slow Kitchen",
		videos: []spec{
			{title: "A loaf, start to finish", seconds: 75, kind: "videos", codec: "avc1", height: 1080, chapters: true, sponsors: true},
			{title: "Stock, and what to do with it", seconds: 50, kind: "videos", codec: "avc1", height: 720, chapters: true},
			// The one nothing Apple can decode directly: playing it has to go
			// through the compatible HLS rendition.
			{title: "Knife skills (VP9 source)", seconds: 40, kind: "videos", codec: "vp09", height: 720},
		},
	},
	{
		id:   "UC-fake-signals",
		name: "Signals & Noise",
		videos: []spec{
			{title: "What a Fourier transform is for", seconds: 80, kind: "videos", codec: "avc1", height: 1080, chapters: true},
			{title: "Live: filter design questions", seconds: 60, kind: "streams", codec: "avc1", height: 720},
			{title: "Aliasing, seen", seconds: 35, kind: "videos", codec: "avc1", height: 1080, sponsors: true},
		},
	},
	{
		id:   "UC-fake-tapes",
		name: "Field Tapes",
		videos: []spec{
			{title: "Harbour at six in the morning", seconds: 55, kind: "videos", codec: "avc1", height: 1080},
			{title: "Rain on a tin roof", seconds: 45, kind: "videos", codec: "avc1", height: 720},
			{title: "Night bus", seconds: 40, kind: "videos", codec: "avc1", height: 720},
		},
	},
}

// NewCatalogue builds the fixed catalogue. Everything is derived from the
// specs above, so ids, dates and ordering are the same on every run.
func NewCatalogue() *Catalogue {
	c := &Catalogue{}
	// A fixed "now" would go stale; a fixed *offset* from today keeps the
	// dates plausible without making the fixture depend on the clock.
	published := time.Now().AddDate(0, 0, -1)
	random := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // fixture data, not security

	for _, ch := range channelSpecs {
		c.Channels = append(c.Channels, ta.Channel{
			ChannelID:          ch.id,
			ChannelName:        ch.name,
			ChannelThumbURL:    "/media/" + ch.id + "/folder.jpg",
			ChannelBannerURL:   "/media/" + ch.id + "/banner.jpg",
			ChannelDescription: ch.name + " — a channel in the fake archive.",
			ChannelSubscribed:  true,
			ChannelActive:      true,
			ChannelLastRefresh: published.Format("2006-01-02"),
		})
		for i, s := range ch.videos {
			id := videoID(ch.id, i)
			published = published.AddDate(0, 0, -1-random.IntN(3))
			c.Videos = append(c.Videos, video(ch.id, ch.name, id, s, published))
		}
	}

	// Two channel playlists and one custom one, so both kinds have something
	// behind them.
	c.Playlists = []ta.Playlist{
		c.channelPlaylist("PL-fake-workshop", "Workshop basics", "UC-fake-workshop"),
		c.channelPlaylist("PL-fake-signals", "Signals, in order", "UC-fake-signals"),
		{
			PlaylistID:          "PL-fake-custom",
			PlaylistName:        "Saved for later",
			PlaylistType:        "custom",
			PlaylistDescription: "A custom playlist you can add to and reorder.",
			PlaylistActive:      true,
			PlaylistEntries: []ta.PlaylistEntry{
				{YoutubeID: videoID("UC-fake-kitchen", 0), Title: "A loaf, start to finish", Uploader: "Slow Kitchen", Idx: 0, Downloaded: true},
				{YoutubeID: videoID("UC-fake-tapes", 1), Title: "Rain on a tin roof", Uploader: "Field Tapes", Idx: 1, Downloaded: true},
			},
		},
	}
	return c
}

// videoID is stable and 11 characters like a real YouTube id, which matters:
// clients and the media cache key on it.
func videoID(channelID string, index int) string {
	base := strings.TrimPrefix(channelID, "UC-fake-")
	id := fmt.Sprintf("%s%d", base, index)
	for len(id) < 11 {
		id += "x"
	}
	return id[:11]
}

func video(channelID, channelName, id string, s spec, published time.Time) ta.Video {
	v := ta.Video{
		YoutubeID: id,
		Title:     s.title,
		Channel: ta.Channel{
			ChannelID:       channelID,
			ChannelName:     channelName,
			ChannelThumbURL: "/media/" + channelID + "/folder.jpg",
		},
		VidThumbURL:    "/media/" + channelID + "/" + id + ".jpg",
		MediaURL:       channelID + "/" + id + ".mp4",
		Published:      published.Format("2006-01-02"),
		DateDownloaded: published.Add(2 * time.Hour).Unix(),
		VidType:        s.kind,
		Player:         ta.Player{Duration: s.seconds, DurationStr: durationString(s.seconds)},
		Stats:          ta.Stats{ViewCount: int64(len(s.title)) * 1371, LikeCount: int64(len(s.title)) * 42},
		Streams: []ta.Stream{
			{Type: "video", Codec: s.codec, Width: s.height * 16 / 9, Height: s.height, Bitrate: s.height * 4000},
			{Type: "audio", Codec: "mp4a", Bitrate: 128000},
		},
		Subtitles: []ta.Subtitle{
			{Lang: "en", Source: "user", Name: "English", MediaURL: channelID + "/" + id + ".en.vtt"},
		},
		Active: true,
	}
	v.Description = description(s)
	if s.sponsors {
		// A skip and a mute segment, and the highlight: everything a player
		// has to tell apart. The service knows nothing about these ids, so
		// the backend falls back to exactly this snapshot.
		v.Sponsorblock = ta.Sponsorblock{
			IsEnabled: true,
			Segments: []ta.SponsorSegment{
				{Category: "sponsor", Segment: [2]float64{s.seconds * 0.1, s.seconds * 0.2}},
				{Category: "selfpromo", Segment: [2]float64{s.seconds * 0.8, s.seconds * 0.9}},
			},
		}
	}
	return v
}

// description carries chapter timestamps for the videos that have them, which
// is also what exercises the description-parsing fallback.
func description(s spec) string {
	if !s.chapters {
		return "A video in the fake archive. Nothing here is real."
	}
	var b strings.Builder
	b.WriteString("A video in the fake archive.\n\n")
	for i, c := range chapterMarks(s.seconds) {
		fmt.Fprintf(&b, "%s %s\n", clock(c), []string{"Intro", "The middle bit", "Wrapping up"}[i])
	}
	return b.String()
}

// chapterMarks splits a duration into three, which both the description and
// the embedded chapters use so the two agree.
func chapterMarks(seconds float64) []float64 {
	return []float64{0, seconds / 3, seconds * 2 / 3}
}

func clock(seconds float64) string {
	total := int(seconds)
	return fmt.Sprintf("%d:%02d", total/60, total%60)
}

func durationString(seconds float64) string {
	total := int(seconds)
	if total >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", total/3600, (total%3600)/60, total%60)
	}
	return clock(seconds)
}

func (c *Catalogue) channelPlaylist(id, name, channelID string) ta.Playlist {
	p := ta.Playlist{
		PlaylistID:          id,
		PlaylistName:        name,
		PlaylistChannel:     c.channelName(channelID),
		PlaylistChannelID:   channelID,
		PlaylistType:        "regular",
		PlaylistDescription: "Everything from " + c.channelName(channelID) + ", in order.",
		PlaylistActive:      true,
	}
	for _, v := range c.Videos {
		if v.Channel.ChannelID != channelID {
			continue
		}
		p.PlaylistEntries = append(p.PlaylistEntries, ta.PlaylistEntry{
			YoutubeID:  v.YoutubeID,
			Title:      v.Title,
			Uploader:   v.Channel.ChannelName,
			Idx:        len(p.PlaylistEntries),
			Downloaded: true,
		})
	}
	if len(p.PlaylistEntries) > 0 {
		p.PlaylistThumbnail = "/media/" + channelID + "/" + p.PlaylistEntries[0].YoutubeID + ".jpg"
	}
	return p
}

func (c *Catalogue) channelName(id string) string {
	for _, ch := range c.Channels {
		if ch.ChannelID == id {
			return ch.ChannelName
		}
	}
	return id
}

// specFor finds the generator spec behind a video id.
func specFor(id string) (spec, string, bool) {
	for _, ch := range channelSpecs {
		for i, s := range ch.videos {
			if videoID(ch.id, i) == id {
				return s, ch.id, true
			}
		}
	}
	return spec{}, "", false
}
