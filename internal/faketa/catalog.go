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
	title   string
	seconds float64
	kind    string // videos|shorts|streams
	codec   string // avc1|vp09 — vp09 is what makes a client reach for the HLS rendition
	height  int
	// width is the coded width. 0 means 16:9 from the height, which is what
	// nearly every archived video is. A spec that sets it is buying the one
	// thing 16:9 cannot exercise: anything derived per-frame has to fit a
	// still into a cell it did not choose, and a scrub-preview sheet built on
	// the assumption that it never has to was wrong for every other ratio.
	width    int
	chapters bool
	sponsors bool
	// levelDB moves the generated tone, in decibels, so the archive is not one
	// uniform wall of sine at one level. It is what makes loudness
	// normalisation visible: the measured loudness of a video, and therefore
	// the gain the server hands a player, follows this number. lavfi's `sine`
	// peaks at -18 dBFS and measures about -22 LUFS, so these are mostly
	// *boosts*, and a video is loud or quiet relative to the -18 LUFS target
	// the server normalises toward.
	levelDB float64
}

// codedWidth is the width the generator encodes and the document reports —
// 16:9 unless the spec says otherwise.
func (s spec) codedWidth() int {
	if s.width > 0 {
		return s.width
	}
	return s.height * 16 / 9
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
			{title: "Building a dovetail jig", seconds: 90, kind: "videos", codec: "avc1", height: 1080, chapters: true, sponsors: true, levelDB: 8},
			{title: "Sharpening, properly", seconds: 60, kind: "videos", codec: "avc1", height: 720, chapters: true, levelDB: 0},
			{title: "Shop tour 2026", seconds: 45, kind: "videos", codec: "avc1", height: 1080, sponsors: true, levelDB: 14},
			{title: "One-minute finish test", seconds: 30, kind: "shorts", codec: "avc1", height: 720, levelDB: 5},
		},
	},
	{
		id:   "UC-fake-kitchen",
		name: "Slow Kitchen",
		videos: []spec{
			{title: "A loaf, start to finish", seconds: 75, kind: "videos", codec: "avc1", height: 1080, chapters: true, sponsors: true, levelDB: 10},
			{title: "Stock, and what to do with it", seconds: 50, kind: "videos", codec: "avc1", height: 720, chapters: true, levelDB: 2},
			// The one nothing Apple can decode directly: playing it has to go
			// through the compatible HLS rendition.
			{title: "Knife skills (VP9 source)", seconds: 40, kind: "videos", codec: "vp09", height: 720, levelDB: 6},
			// Long enough to *resume* into. A video shorter than a couple of
			// minutes is marked seen the moment playback gets anywhere near
			// its end, so it can never carry a resume position — which is
			// exactly what "does the transcode start where the viewer left
			// off, or from the beginning?" needs to be answered by eye.
			{title: "Braising, the long way (VP9 source)", seconds: 600, kind: "videos", codec: "vp09", height: 720, chapters: true, levelDB: 7},
		},
	},
	{
		id:   "UC-fake-signals",
		name: "Signals & Noise",
		videos: []spec{
			{title: "What a Fourier transform is for", seconds: 80, kind: "videos", codec: "avc1", height: 1080, chapters: true, levelDB: -1},
			{title: "Live: filter design questions", seconds: 60, kind: "streams", codec: "avc1", height: 720, levelDB: 12},
			{title: "Aliasing, seen", seconds: 35, kind: "videos", codec: "avc1", height: 1080, sponsors: true, levelDB: 3},
		},
	},
	{
		id:   "UC-fake-tapes",
		name: "Field Tapes",
		videos: []spec{
			{title: "Harbour at six in the morning", seconds: 55, kind: "videos", codec: "avc1", height: 1080, levelDB: -3},
			{title: "Rain on a tin roof", seconds: 45, kind: "videos", codec: "avc1", height: 720, levelDB: 9},
			{title: "Night bus", seconds: 40, kind: "videos", codec: "avc1", height: 720, levelDB: 1},
			// The one video in the archive that is not 16:9, and long enough
			// for its preview sheet to run to several rows — the shape of
			// source that turned a scrubber black.
			{title: "Cliffs at low tide (2.40:1 source)", seconds: 90, kind: "videos", codec: "avc1", height: 800, width: 1920, levelDB: 4},
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

	// Two whole-channel playlists, one *partial* one and a custom one. The
	// partial playlist is the point of the series feature: a feed holding it
	// must show fewer videos than one holding its channel, which is only
	// checkable when the two sets differ.
	c.Playlists = []ta.Playlist{
		c.channelPlaylist("PL-fake-workshop", "Workshop basics", "UC-fake-workshop"),
		c.channelPlaylist("PL-fake-signals", "Signals, in order", "UC-fake-signals"),
		c.partialPlaylist("PL-fake-night", "Night sides", "UC-fake-tapes", 2),
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
	// A real TubeArchivist stamps playlist membership onto each video
	// document; the codepaths that read it (feed sources, search scoping)
	// need the fake to do the same.
	byID := map[string]int{}
	for i, v := range c.Videos {
		byID[v.YoutubeID] = i
	}
	for _, p := range c.Playlists {
		if p.PlaylistType == "custom" {
			continue
		}
		for _, e := range p.PlaylistEntries {
			if i, ok := byID[e.YoutubeID]; ok {
				c.Videos[i].Playlist = append(c.Videos[i].Playlist, p.PlaylistID)
			}
		}
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
			{Type: "video", Codec: s.codec, Width: s.codedWidth(), Height: s.height, Bitrate: s.height * 4000},
			{Type: "audio", Codec: "mp4a", Bitrate: 128000},
		},
		Subtitles: []ta.Subtitle{
			{Lang: "en", Source: "user", Name: "English", MediaURL: channelID + "/" + id + ".en.vtt"},
		},
		Active: true,
	}
	v.Description = description(s)
	if s.sponsors {
		// A skip and a mute segment, the highlight, and an intro: everything a
		// player has to tell apart. The intro is what makes the *manual* skip
		// reachable — its category defaults to "ask", so it is offered rather
		// than jumped, and a button nobody can trigger is a button nobody can
		// check. The service knows nothing about these ids, so the backend
		// falls back to exactly this snapshot.
		v.Sponsorblock = ta.Sponsorblock{
			IsEnabled: true,
			Segments: []ta.SponsorSegment{
				{Category: "intro", Segment: [2]float64{0, s.seconds * 0.08}},
				{Category: "sponsor", Segment: [2]float64{s.seconds * 0.1, s.seconds * 0.2}},
				{Category: "selfpromo", Segment: [2]float64{s.seconds * 0.8, s.seconds * 0.9}},
			},
		}
	}
	return v
}

// description carries chapter timestamps for the videos that have them, which
// is also what exercises the description-parsing fallback — and, under them,
// the rest of what a real description is made of: links, one of them wrapped
// in brackets and one with no scheme, and enough lines to go under a fold.
// A description that fits in four lines never shows the "Show more" that
// every real one has.
func description(s spec) string {
	if !s.chapters {
		return "A video in the fake archive. Nothing here is real."
	}
	var b strings.Builder
	b.WriteString("A video in the fake archive.\n\n")
	for i, c := range chapterMarks(s.seconds) {
		fmt.Fprintf(&b, "%s %s\n", clock(c), []string{"Intro", "The middle bit", "Wrapping up"}[i])
	}
	b.WriteString("\nPlans and the full cut: https://example.com/plans (the jig itself: https://en.wikipedia.org/wiki/Jig_(tool)).\n")
	b.WriteString("Music by www.example.org, used with permission.\n")
	b.WriteString("Gear list, which is long and a single link: https://example.com/gear/list?items=saw,plane,chisel,marking-gauge,mallet,square,bench-hook&sort=used-most&format=long\n")
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

// partialPlaylist is a series: the first n of a channel's videos, so a feed
// sourcing it differs visibly from one sourcing the channel.
func (c *Catalogue) partialPlaylist(id, name, channelID string, n int) ta.Playlist {
	p := c.channelPlaylist(id, name, channelID)
	if len(p.PlaylistEntries) > n {
		p.PlaylistEntries = p.PlaylistEntries[:n]
	}
	p.PlaylistDescription = "A series inside " + c.channelName(channelID) + ": part of the channel, not all of it."
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

// Branding is the fake's DeArrow data for one video: what the crowd would have
// said about it. Only some videos have any, which is the interesting case —
// "manual" has to leave the rest alone, and "all" has to fill them in.
type Branding struct {
	// Title a person submitted, or "" for none.
	Title string
	// TitleOriginal marks a crowd that voted to keep the uploader's title.
	TitleOriginal bool
	// ThumbnailAt is the second of the video someone picked, or nil for none.
	ThumbnailAt *float64
	// RandomTime is what DeArrow would suggest on its own, as a fraction.
	RandomTime float64
}

// brandingFor is the fake's opinion about each video. Deliberately a mix:
//
//   - a shouted title with a submitted replacement *and* a submitted frame,
//   - a shouted title nobody has touched, which only "all" tidies,
//   - a video where the crowd voted to keep the original,
//   - and everything else, which has nothing at all.
func brandingFor(videoID string) (Branding, bool) {
	at := func(s float64) *float64 { return &s }
	switch videoID {
	case "workshop0xx":
		return Branding{Title: "Building a dovetail jig, start to finish", ThumbnailAt: at(21), RandomTime: 0.4}, true
	case "kitchen0xxx":
		return Branding{ThumbnailAt: at(33), RandomTime: 0.2}, true
	case "signals0xxx":
		return Branding{TitleOriginal: true, RandomTime: 0.6}, true
	case "tapes0xxxxx":
		return Branding{RandomTime: 0.35}, true
	}
	return Branding{}, false
}
