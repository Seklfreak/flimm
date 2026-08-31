# Product model

Flimm is a viewing client for one TubeArchivist instance. TubeArchivist does
the downloading, indexing and storage; Flimm adds a per-user way to *watch*
what it holds. The design canvas (screens for web, iPhone, iPad and Apple TV)
lives outside this repo; this page is the written summary of the model it
encodes.

## Feeds

A **feed** is a named set of sources — *Home*, *DevOps*, *Making*, *Cooking*.
A source is usually a whole channel, but it can be a single playlist — a
**series** — for channels that publish everything into series: one Let's Play
out of a channel with thousands of videos, without following the rest. The
feed's videos are the union of its sources. Feeds are the primary way of
browsing and are separate from the channel directory:

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
  already in), a series picker behind each channel (its archived playlists,
  fetched only when opened; redundant while the whole channel is selected) and
  the options above. Deleting a feed never deletes channels or videos.
- Playlist pages carry the same *In feeds: …* control a channel page does, so
  a series found while browsing can be put in a feed on the spot. Being a
  feed source does **not** put a channel playlist on the Playlists page —
  that page stays custom + taken-up only.
- A feed view can show *unseen*, *continue watching* or *all*, and has a
  *mark all seen* action.

## Channels

**Channels** is the full directory: search, sort (name, videos, unseen, last
upload), see which feeds each channel is in, and spot channels that are in no
feed. Any channel name on a card links to its channel page, which lists all its
videos and playlists and has an *In feeds: …* control to add or remove it from
feeds without leaving the page. A channel can be **pinned**, exactly like a
playlist: it joins the sidebar (web) or a *Pinned* section leading the
directory (iPhone, iPad, Apple TV), with its unseen count, and unpinning never
touches anything but the pin.

## Player, resume and seen state

- **"Not interested" takes a video out of the feeds without watching it.**
  Marking something seen to clear a feed lies about the watch state, and Flimm
  writes that back to TubeArchivist, so it would follow the viewer into TA's
  own UI and every other client. Dismissing is Flimm's own per-user state and
  says nothing about playback. It applies to *every* feed (including
  Everything, in every view) and to *up next*, so autoplay never plays
  something that was dismissed. Channel pages, playlists, search and history
  still show it, marked, which is where a viewer puts one back. Every client
  offers an undo without navigating away, because the action is one tap from a
  card and easy to hit by accident.
- **An unseen feed opens with what you are part-way through**, most recently
  played first, and then runs into the rest of the unseen videos. Those are
  the ones a viewer came back for, which is why there is no separate "in
  progress" filter to go and find them in.
- **Resume is automatic**, and starts 15 seconds before where playback
  stopped, because landing in the middle of a sentence costs more than the
  seconds do. A chip in the top-left says where playback resumed from, with
  *Start over*. It is an offer rather than a status, so it retires
  itself after a minute of playback past the resume point — measured in
  playback, so pausing to decide does not spend the minute. Position is
  reported by heartbeat while playing.
- A video flips to **seen** automatically at about 90 % (or with 30 s left).
  *Mark seen* is the primary action under the video; *Mark unseen* clears the
  position.
- **Watching something again makes it unseen again**, once you are far enough
  in to mean it. Seen is a statement about where you are in a video, not a
  medal it keeps: a video you cannot resume because you finished it once is a
  video that starts at 0:00 every time you come back to it. Glancing at one for
  a few seconds leaves it seen.
- Seen state and resume position are written back to TubeArchivist, so the
  stock TA UI stays consistent.
- **The archived comments are there if you want them, and cost nothing if you
  don't.** They come from the copy TubeArchivist downloaded — nothing is
  fetched from YouTube, and no avatar is loaded from Google's CDN, because a
  picture of a stranger is not worth telling a third party what you watch;
  clients draw an initial. Every client keeps them folded away until asked,
  and asking is what loads them: they are the longest thing attached to a
  video and the least often wanted. Replies fold separately, under their
  count.
- **The Apple TV's Home screen offers the pinned feed.** Focusing Flimm in the
  top row shows what is waiting in it, with a resume bar on anything
  part-watched, and selecting one plays it — the same list the app opens on,
  one step earlier. It is what the app last saw rather than a live query: the
  row is drawn by tvOS in a process that has no session, so the app leaves it
  ready. Signing out takes it down.
- **Videos play at an even volume.** Each one is measured once against a
  broadcast loudness target, and the loud ones are turned down to meet it, so
  moving between channels stops meaning reaching for the volume. Only *down*:
  nothing is amplified, because the platforms cannot all do it and a video that
  sounded louder on the web than on the TV would be a worse problem than the
  one being fixed. Quiet videos are left as they are. One switch turns it off,
  and it is on by default — it asks nobody anything and changes nothing about
  what a video *is*.
- **Dragging the scrubber shows the frame you are dragging to**, so finding a
  moment is looking rather than guessing-and-seeking. The stills are derived
  from the deployment's own copy of the video, once per video and only once
  someone is actually watching it: it is the most expensive thing the server
  derives, and a video nobody scrubs never costs it. Apple TV is the exception,
  and a deliberate one — its scrubber belongs to AVKit, which draws its own
  stills and takes none from us.
- The subtitle picker is the CC button in the controls: Off, archived tracks,
  and auto-generated tracks marked *auto*, plus size. Cues are white on a
  dark plate — tried without one, and the plate is what makes them readable
  over a bright scene — and they sit clear of the bottom edge and of whatever
  transport controls are up, on every client. SponsorBlock segments
  are skipped, offered as a *Skip the intro* button, or left alone, per
  category (a preference) — and a segment its contributor
  marked *mute* rather than *skip* is muted for its length instead, because
  the picture still matters there. Segments come from the SponsorBlock service
  through the backend, not from TubeArchivist's download-time snapshot, so
  they stay current; the same lookup supplies chapter names for videos whose
  file carries none. What each category does is the viewer's: sponsor,
  self-promo and interaction reminders are skipped by default, and the ones
  that are sometimes the point — intro, outro, recap, filler — are *offered*
  by default, because an intro is occasionally exactly what someone came for.
  Every client reads the same setting, so a category changed on the phone
  behaves that way on the TV tonight.
- **Titles and thumbnails can come from DeArrow**, the crowd-sourced companion
  to SponsorBlock, and the two are separate settings: a viewer may trust what
  people wrote and not the frames they picked. *Manual* uses submissions only;
  *All* also tidies a shouted title and takes the frame DeArrow suggests where
  nobody submitted one. Both are **off** by default — rewriting what every
  video is called is a strong opinion to hold on someone's behalf. A
  crowd-sourced thumbnail is cut from the deployment's own copy of the video,
  because DeArrow hands out a timestamp rather than an image: no third-party
  image fetch, and it works with the archive offline. The server applies both,
  so a video is called the same thing on the phone and on the TV.
- **The highlight** — where a contributor marked "this is where the video
  actually starts" — is a marker on the timeline and a *Jump to the highlight*
  control while playback is still before it. It is never taken automatically,
  and it is offered whether or not sponsor skipping is on: jumping is a
  choice, not a skip.
- **Quality is a per-device choice**, next to speed and subtitles: *Auto*,
  then the qualities this video offers. The archive holds whatever was
  downloaded — often AV1 or VP9, which some devices and browsers decode and
  others do not — so the server can derive a compatible rendition at several
  heights, and every platform picks from the same ladder by the same rule:

  - *Auto* plays the **archived file** whenever the device can decode it: the
    original, full quality, and nothing for the server to transcode. Only when
    it cannot does Auto fall to a rendition — the tallest one the screen can
    actually show, skipping any in a codec the device lacks.
  - An **explicit height** wins even over a playable archive, because "720p"
    is a request for less data, not a mistake. A height at or above the
    source's own is the archive again.
  - Switching mid-play keeps the position, and a rendition that has produced
    nothing yet says *Preparing a compatible version… 37 %* rather than
    stalling silently.

  The choice lives on the device, not in the account: a phone on cellular and
  a desktop on a 4K panel want different answers from the same login.
- *Up next* follows the context the video was opened from (feed, playlist or
  channel), falling back to similar videos. The same panel can unfold
  **Previous** — what came before the video in that context, closest first —
  so a playlist opened in the middle can be walked backwards too. It offers *Not interested* like
  any other list — the video leaves the list, since up next never contains a
  dismissed one, and the slot it leaves behind is the way back. On the web the
  whole sidebar collapses, remembered per browser: a layout choice the phone
  and TV have no equivalent for.

## Playlists

Your own custom playlists, plus the channel playlists you have taken up —
pinned, or marked as music. A channel's full set of playlists lives on its
channel page instead: the archive may index every playlist a prolific channel
owns, and a page that listed them all would bury the lists that are yours
under hundreds that are not. Taking one up is what puts it here. Each card
shows watched progress across the playlist and a resume target (the first
in-progress video, else the first unseen). Custom playlists are created,
reordered and deleted through TubeArchivist so they exist there too.

## History

Grouped by day, newest first. In-progress rows resume in place — and resume
*into the feed the video belongs to* when one holds it, so the player's up
next shows that feed instead of similar videos; the sidebar's continue-watching
rail does the same. A video that got there through a **series** (a feed's
playlist source) resumes into the series itself — up next is the next episode
— which beats any feed that merely holds the channel: the series is the run
being watched, the channel just its uploader. Seen rows are listed for
reference. The × on a row hides the entry without changing the
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

**Every client has a Settings screen carrying all of them** — the web sidebar
(and, on a narrow window, a gear beside the title), the phone and iPad's
Settings tab, and the Apple TV's. A preference reachable on one client and not
another is a bug: the account is shared, so a viewer who turns sponsor skipping
off at their desk expects it off on the TV that evening.

Video quality is deliberately **not** one of them — it belongs to the screen
and the network in front of it, so it is kept on the device (the browser's
`localStorage`, `UserDefaults` on Apple platforms) and never synced.

## Platforms

Web first; native iOS, iPadOS and tvOS apps follow the same model and talk to
the same API (see [apple-apps.md](apple-apps.md)). On Apple platforms the
mobile layout is the iPhone app (tab bar: Feeds · Channels · Playlists ·
History, search in the header), iPad gets a persistent sidebar and a
three-column grid, and Apple TV merges feeds and library into a top tab bar
with a focus-driven, full-bleed player.

The iPad's columns follow the window rather than the device: three across a
full-width iPad, fewer in Split View, and a pane narrow enough (Slide Over)
falls back to the phone's tab bar. Where the width allows it the player keeps
the chapter list and up next beside the video instead of under it.
