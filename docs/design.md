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
- A feed can also **watch a channel for new series** without carrying its
  videos: when the archive indexes a playlist the viewer has never seen, the
  feed announces it once — a card above the videos — until the viewer
  subscribes the series (it becomes a playlist source) or dismisses it.
  Watching starts from *now*: the channel's existing playlists are baselined
  away, so only genuinely new series announce.
- A feed can **notify**: "tell me when this feed has something new." The
  server polls the archive for what it *downloaded* for the feed's sources —
  not what YouTube published; a backfill of old uploads is news to the
  archive — and pushes one notification per feed to the viewer's iPhones and
  iPads: a single video by name, opened in its feed when tapped, or a digest
  that opens the feed. Like a series watch it starts from *now*, so switching
  it on over a big channel announces the next download, not the backlog.
  Watched and dismissed videos are not news. The web editor can set the flag
  and says how many devices it reaches; Apple TV has no part in it — tvOS
  shows no banners, and the top shelf already carries the pinned feed.
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
touches anything but the pin. An admin can also flip the archive's own
**subscription** from the channel page — whether TubeArchivist keeps
downloading the channel's new videos — and subscribe a **brand-new channel**
from the directory (URL, @handle or id; the archive resolves it in the
background), all without visiting TA's UI. Both are instance-wide state,
which is why they are admin-gated like series indexing.

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
- **A context that has run out says so, and its suggestions say what they
  are.** At the end of a playlist or feed the server still offers TA's similar
  videos, but marked (`suggestions` in `docs/api.md`) and with anything
  already watched or dismissed taken out. No client autoplays into them, the
  end card does not offer one as "up next", and the panel heads them "Similar
  videos" instead of keeping the playlist's name over them: the whole point of
  a queue is that it is the list you chose, and five episodes you just watched
  offered back under that heading is what "up next" stopped meaning anything
  looks like. A watched video that does appear in a list is dimmed the same
  way wherever it appears, queue or history.
- **The work a viewer would wait for happens before they arrive.** Everything
  Flimm derives is made on first request, which makes the first view of any
  video the worst one: a bare scrubber, and whatever level the video was
  archived at. So the two derivations that are cheap enough to do speculatively
  — the preview sheet and the loudness measurement — are made ahead of time for
  the head of every feed. What is *not* done speculatively is the point: a
  transcode is a thousand times the disk of a preview sheet, and preparing
  feeds' worth of them is terabytes, so renditions stay on demand. The job
  stops while anything is playing, because a viewer's picture is worth more
  than a stranger's future scrubber.
- **The player can say what it is doing.** Almost everything Flimm derives is
  invisible on purpose — a transcode the viewer never asked for, a sheet of
  stills queued behind it, a measurement that quietly turns the volume down —
  and each of them fails in a way that looks exactly like nothing happening.
  A **playback stats** panel under the video says which of them are running
  and how far along, which are ready, and above all whether what is on screen
  is the archived file or an encode the server is paying for, *and why the
  gate chose that*. Every derivation counts up the same way — "deriving · 42%"
  means the same thing whether it came from a transcode counting its own
  segments or a scan reading ffmpeg's frame counter — because a wait with no
  number on it is indistinguishable from a wedge. Opening the panel is also
  what makes those numbers *move*: the scans are polled on gaps that grow to a
  minute, sized for nobody waiting on the answer, and while someone is reading
  them they are asked for every second and a half instead.
  It reads and never decides: every line comes from the value the player
  itself runs on, so it cannot disagree with the picture it describes. Web
  only for now — a deliberate scope, not a platform left behind: the
  questions it answers are the ones asked while looking at a page with a
  console open, and the Apple clients report the same conditions through
  Sentry and the stall reports instead.
- **A video that ends says so.** Autoplay advances only when there is
  something to advance to; any other ending — autoplay off, or the end of the
  list — leaves the player sitting on its last frame, which is exactly what a
  paused one looks like. So the player raises an end card instead: "Finished",
  what plays next when there is one, and a Replay that rewinds without
  clearing the watch state the video has just earned. The rule lives in one
  place per platform (`PlaybackEnd` in FlimmKit,
  `frontend/src/player/playbackEnd.ts`) so the page that navigates and the
  player that raises the card cannot disagree. On the Apple TV the card
  states rather than offers: AVKit's transport bar underneath owns focus, and
  it already holds previous/next and the scrubber.
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
  clients draw an initial. They sit open under the description, because that
  is where what is said about a video belongs, and closing the section is
  remembered for the session so nobody closes it twice; while it is closed
  nothing is loaded. Replies fold separately, under their count, and a long
  comment folds like the description does, so one essay cannot push every
  other comment off the screen.
- **Descriptions and comments are read, not just shown.** A URL in either
  opens; a timestamp seeks, which is the one thing a video's own page can do
  with "the bit at 2:30" that YouTube's cannot. What counts as either is one
  rule shared by every client (`lib/richText` on the web, `RichText` in
  FlimmKit), including that a timestamp past the end of the video is only
  text. The description opens folded to a few lines with a "Show more":
  it is a paragraph and then a wall of links, and the wall goes under the
  fold.
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
  channel), falling back to similar videos. The same panel always carries
  **Previous** — what came before the video in that context, closest first,
  watched entries dimmed: two rows tall, with the history further back a
  scroll (web) or a *Show earlier* (phone) away — so a playlist opened in the
  middle can be walked backwards too. It offers *Not interested* like
  any other list — the video leaves the list, since up next never contains a
  dismissed one, and the slot it leaves behind is the way back. On the web the
  whole sidebar collapses, remembered per browser: a layout choice the phone
  and TV have no equivalent for.

## Playlists

The playlists that are yours: the custom ones you made in TubeArchivist, the
YouTube playlists you subscribed to there, and the channel playlists you have
taken up here — pinned, or marked as music. What is left out is the rest of
what a channel owns, which lives on its channel page instead: the archive may
index every playlist a prolific channel has, and a page that listed them all
would bury the lists that are yours under hundreds that are not. Choosing a
playlist — anywhere — is what puts it here. Pinned
playlists lead the page in a section of their own and are not repeated in the
list below it — a pin is a shortcut to a playlist, not a second playlist.
Each card shows watched progress across the playlist and a resume target
(the first in-progress video, else the first unseen). Custom playlists are created,
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

When two of them are in the same room they are one thing: a video playing on
the Apple TV shows up on the phone and the iPad, which can pause it, scrub it,
step through it, and — the reason it is worth having — show the description and
the comments, which are unreadable at two metres and ordinary in the hand. It
is the account that connects them, not the network, and nothing is paired. The
phone steers what the television started; it does not send it anything to play.

The iPad's columns follow the window rather than the device: three across a
full-width iPad, fewer in Split View, and a pane narrow enough (Slide Over)
falls back to the phone's tab bar. Where the width allows it the player keeps
the chapter list and up next beside the video instead of under it.
