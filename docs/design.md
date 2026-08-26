# Product model

Flimm is a viewing client for one TubeArchivist instance. TubeArchivist does
the downloading, indexing and storage; Flimm adds a per-user way to *watch*
what it holds. The design canvas (screens for web, iPhone, iPad and Apple TV)
lives outside this repo; this page is the written summary of the model it
encodes.

## Feeds

A **feed** is a named set of channels — *Home*, *DevOps*, *Making*, *Cooking*.
Feeds are the primary way of browsing and are separate from the channel
directory:

- Feeds live at the top of the sidebar (desktop) or behind the title dropdown
  (mobile). Each shows its unseen count. One feed can be **pinned**; that is
  the feed the app opens on.
- **Everything** is the built-in feed over all channels. It is always last and
  read-only except for its sort / hide-seen / include-Shorts options, which are
  stored in the user's prefs.
- Feed options: sort (newest, oldest, shortest, longest), hide seen by default,
  include Shorts, only videos with subtitles, pin to top.
- The feed editor is one form for *New feed* and *Edit feed*: name, a channel
  picker (search, select all / clear, shows which other feeds each channel is
  already in) and the options above. Deleting a feed never deletes channels or
  videos.
- A feed view can show *unseen*, *continue watching* or *all*, and has a
  *mark all seen* action.

## Channels

**Channels** is the full directory: search, sort (name, videos, unseen, last
upload), see which feeds each channel is in, and spot channels that are in no
feed. Any channel name on a card links to its channel page, which lists all its
videos and playlists and has an *In feeds: …* control to add or remove it from
feeds without leaving the page.

## Player, resume and seen state

- **Resume is automatic.** A chip in the top-left says where playback resumed
  from, with *Start over*. Position is reported by heartbeat while playing.
- A video flips to **seen** automatically at about 90 % (or with 30 s left).
  *Mark seen* is the primary action under the video; *Mark unseen* clears the
  position.
- Seen state and resume position are written back to TubeArchivist, so the
  stock TA UI stays consistent.
- The subtitle picker is the CC button in the controls: Off, archived tracks,
  and auto-generated tracks marked *auto*, plus size. SponsorBlock segments
  can be skipped automatically (a preference).
- *Up next* follows the context the video was opened from (feed, playlist or
  channel), falling back to similar videos.

## Playlists

Your own custom playlists and the ones archived from channels, side by side.
Each card shows watched progress across the playlist and a resume target (the
first in-progress video, else the first unseen). Custom playlists are created,
reordered and deleted through TubeArchivist so they exist there too.

## History

Grouped by day, newest first. In-progress rows resume in place; seen rows are
listed for reference. The × on a row hides the entry without changing the
video's seen state. Filter by in-progress / seen and search by title or channel.

## Search

Search covers **titles, channel names, playlist names and subtitle text**
(TubeArchivist indexes subtitles). Subtitle hits show the timestamp and a
snippet; tapping one starts playback at that moment. Scope chips narrow the
results to one kind, to unseen videos, or to the current feed.

## Preferences

Autoplay, playback speed, subtitle language and size, skip sponsors, the
Everything-feed options and theme (system / light / dark). Stored per user in
Flimm.

## Platforms

Web first; native iOS, iPadOS and tvOS apps follow the same model and talk to
the same API (see [roadmap](roadmap.md)). On Apple platforms the mobile layout
is the iPhone app (tab bar: Feeds · Channels · Playlists · History, search in
the header), iPad gets a persistent sidebar and a three-column grid, and Apple
TV merges feeds and library into a top tab bar with a focus-driven,
full-bleed player.

The iPad's columns follow the window rather than the device: three across a
full-width iPad, fewer in Split View, and a pane narrow enough (Slide Over)
falls back to the phone's tab bar. Where the width allows it the player keeps
the chapter list and up next beside the video instead of under it.
