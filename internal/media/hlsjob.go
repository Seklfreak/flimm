package media

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Resume-first transcoding.
//
// The old shape of this job was one ffmpeg reading the source on stdin and
// growing an EXT-X-PLAYLIST-TYPE:EVENT playlist. It made a viewer who was
// resuming at 40:00 start at 0:00: a player cannot seek past the end of the
// playlist it holds, and the playlist only reached 40:00 once the encoder did.
//
// Now the segment grid is fixed and the playlist is written up front (see
// hlsplaylist.go), so a player can seek anywhere from the first request, and
// the encoder is aimed at whatever the viewer actually asked for:
//
//   - a job carries a `from`, and the first run starts at the segment that
//     position falls in — segment 600 for 40:00 — with the part before it left
//     for a second run;
//   - a request for a segment far ahead of where the encoder is (a mid-playback
//     seek) restarts the run there, keeping everything already produced;
//   - the job is finished when all N segments exist, in whatever order they
//     were filled in.
//
// A run cancelled to be re-aimed loses only the segment it was in the middle
// of, which the muxer's temp_file flag was still holding as a `.tmp` — nothing
// already published is thrown away.

const (
	// DefaultSeekAheadSegments is MEDIA_SEEK_AHEAD_SEGMENTS' default: how far
	// ahead of the encoder a segment request has to be before the run is
	// re-aimed at it. 30 segments is two minutes — comfortably more than the
	// buffer a player reads ahead, so ordinary playback never triggers it and
	// a real seek always does.
	DefaultSeekAheadSegments = 30
	// hlsSteerInterval bounds re-aiming to one restart per ten seconds. A
	// player scrubbing a timeline fires a burst of segment requests, and
	// restarting on each of them would encode nothing at all.
	hlsSteerInterval = 10 * time.Second
	// hlsWatchInterval is how often a running job re-reads its directory to
	// find out where the encoder has got to. ffmpeg gives no signal when it
	// publishes a segment, and one ReadDir is nothing next to the encode.
	hlsWatchInterval = 500 * time.Millisecond
	// hlsRunPlaylistGlob matches the per-run playlists ffmpeg writes. They are
	// read for their segment durations and never served.
	hlsRunPlaylistGlob = "run-*.m3u8"
	// hlsEncoderMarker records which rung of the ladder produced the segments
	// in this entry, so a job resuming a partial directory after a restart
	// carries on with the same encoder. Segments from two different encoders
	// under one init segment decode to garbage.
	hlsEncoderMarker = ".encoder"
)

// errRunSteered marks a run this job cancelled on purpose, to re-aim it at a
// segment a client asked for. It is not a failure: the run's output stays and
// the loop plans again.
var errRunSteered = errors.New("hls: run re-aimed at a requested segment")

// HLSConfig is everything one rendition's job needs. It is a struct because
// the list is long and every field is load-bearing; a positional call would be
// unreadable.
type HLSConfig struct {
	FFmpegPath string
	Source     HLSSource
	Height     int
	HW         HWAccel
	Log        *slog.Logger
	// Open reads the archived file, honouring Range. It is proxied to ffmpeg
	// over loopback so the TA token stays server-side; see loopback.go.
	Open RangeSourceFunc
	// Registry publishes the running job so a segment request can steer it and
	// the API can report progress. nil disables both.
	Registry *HLSRegistry
	// From is the playback position, in seconds, the client wants first. 0
	// starts at the beginning, as before.
	From float64
	// SeekAheadSegments is MEDIA_SEEK_AHEAD_SEGMENTS; 0 uses the default.
	SeekAheadSegments int
}

// HLS returns the two halves of a rendition's derivation.
//
// prepare runs before the job queues for a transcode slot: it works out how
// many segments the rendition has and writes the complete playlist, so a client
// gets a seekable playlist immediately even while the job is waiting behind
// another transcode. derive does the encoding.
func HLS(cfg HLSConfig) (prepare, derive DirDeriveFunc) {
	var (
		mu  sync.Mutex
		job *HLSJob
	)
	get := func(ctx context.Context, dir string) (*HLSJob, error) {
		mu.Lock()
		defer mu.Unlock()
		if job != nil {
			return job, nil
		}
		j, err := newHLSJob(ctx, cfg, dir)
		if err != nil {
			return nil, err
		}
		job = j
		return job, nil
	}
	prepare = func(ctx context.Context, dir string) error {
		j, err := get(ctx, dir)
		if err != nil {
			return err
		}
		j.rescan()
		if err := j.writePlaylist(); err != nil {
			return err
		}
		j.publish()
		return nil
	}
	derive = func(ctx context.Context, dir string) error {
		j, err := get(ctx, dir)
		if err != nil {
			return err
		}
		defer j.retire()
		return j.run(ctx)
	}
	return prepare, derive
}

// HLSJob is a rendition being derived. Everything exported on it is what a
// request handler needs: how far along it is, and which segment a client is
// waiting for.
type HLSJob struct {
	cfg      HLSConfig
	dir      string
	duration float64
	total    int
	ahead    int
	log      *slog.Logger

	mu        sync.Mutex
	produced  map[int]bool
	requested int
	runStart  int
	runEnd    int
	runPos    int
	runCancel context.CancelFunc
	steered   bool
	lastSteer time.Time
	unpublish func()
}

func newHLSJob(ctx context.Context, cfg HLSConfig, dir string) (*HLSJob, error) {
	duration := cfg.Source.Duration
	if duration <= 0 {
		// TA parsed no duration for this video. The grid cannot be guessed, so
		// ask the file itself — over the same loopback source ffmpeg will use,
		// which keeps the token out of ffprobe's argv too.
		probed, err := probeDuration(ctx, cfg.FFmpegPath, cfg.Open, cfg.Log)
		if err != nil {
			return nil, fmt.Errorf("derive hls: no duration for this video: %w", err)
		}
		duration = probed
	}
	total := hlsSegmentCount(duration)
	if total < 1 {
		return nil, fmt.Errorf("derive hls: duration %.3fs is not a video", duration)
	}
	ahead := cfg.SeekAheadSegments
	if ahead < 1 {
		ahead = DefaultSeekAheadSegments
	}
	j := &HLSJob{
		cfg:       cfg,
		dir:       dir,
		duration:  duration,
		total:     total,
		ahead:     ahead,
		log:       cfg.Log,
		produced:  map[int]bool{},
		requested: HLSSegmentIndexAt(cfg.From),
	}
	if j.requested >= total {
		j.requested = -1
	}
	return j, nil
}

// Segments is how many segments the finished rendition has.
func (j *HLSJob) Segments() int {
	if j == nil {
		return 0
	}
	return j.total
}

// Progress is the fraction of the rendition that exists, 0..1.
func (j *HLSJob) Progress() float64 {
	if j == nil || j.total == 0 {
		return 0
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	return math.Round(float64(len(j.produced))/float64(j.total)*1000) / 1000
}

// Has reports whether segment i has been produced.
func (j *HLSJob) Has(i int) bool {
	if j == nil {
		return false
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.produced[i]
}

// RunPosition is the segment the encoder is on, or -1 when nothing is
// running. It is what tells "the viewer stalled waiting for the encoder" apart
// from "the segment was there and something else was slow".
func (j *HLSJob) RunPosition() int {
	if j == nil {
		return -1
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.runCancel == nil {
		return -1
	}
	return j.runPos
}

// RequestSeconds is Request for a playback position rather than a segment
// index — what a client's `from=` means.
func (j *HLSJob) RequestSeconds(seconds float64) {
	if j == nil || seconds <= 0 {
		return
	}
	j.Request(HLSSegmentIndexAt(seconds))
}

// Request records that a client wants segment i, and re-aims the run at it
// when it is far enough ahead of the encoder to be a seek rather than
// read-ahead.
//
// Re-aiming cancels the running ffmpeg. With the muxer's temp_file flag the
// segment being written is a `.tmp` that is simply dropped, so nothing already
// published is lost — which is why this does not wait for the current segment
// to finish before cutting it.
func (j *HLSJob) Request(i int) {
	if j == nil || i < 0 || i >= j.total {
		return
	}
	j.mu.Lock()
	j.requested = i
	cancel := j.runCancel
	switch {
	case j.produced[i], cancel == nil, j.steered:
		j.mu.Unlock()
		return
	case i >= j.runPos && i < j.runPos+j.ahead:
		// Ahead of the encoder but close: it is heading there, and waiting a
		// few seconds is cheaper than restarting.
		//
		// The bound has to be `i >= runPos` as well. A segment *behind* the
		// encoder that is not produced is one this run skipped — everything
		// before a resume point, which the first run leaves for later — and
		// this run will never write it. Treating that as "heading there" left
		// the request waiting for the encoder to finish the rest of the video
		// and wrap around: minutes of nothing, while AVPlayer cancelled every
		// four seconds and asked again. Those are exactly the requests that
		// have to re-aim the run.
		j.mu.Unlock()
		return
	case time.Since(j.lastSteer) < hlsSteerInterval:
		// A scrubbing player fires a burst of these. The index is remembered,
		// so the next run picks it up anyway.
		j.mu.Unlock()
		return
	}
	j.steered, j.lastSteer = true, time.Now()
	log, dir, pos := j.log, j.dir, j.runPos
	j.mu.Unlock()

	if log != nil {
		log.Info("hls run re-aimed", "entry", filepath.Base(dir), "from_segment", pos, "to_segment", i)
	}
	cancel()
}

// publish makes the job findable while it runs.
func (j *HLSJob) publish() {
	if j.cfg.Registry == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.unpublish == nil {
		j.unpublish = j.cfg.Registry.put(filepath.Base(j.dir), j)
	}
}

func (j *HLSJob) retire() {
	j.mu.Lock()
	drop := j.unpublish
	j.unpublish = nil
	j.mu.Unlock()
	if drop != nil {
		drop()
	}
}

// run fills every gap in the rendition, one ffmpeg run at a time, until all N
// segments exist.
func (j *HLSJob) run(ctx context.Context) error {
	j.publish()
	lb, err := newLoopbackSource(j.log)
	if err != nil {
		return err
	}
	defer lb.close()
	src, release := lb.register(j.cfg.Open)
	defer release()

	j.rescan()
	attempts := hlsAttempts(j.cfg.Source, j.cfg.Height, j.cfg.HW)
	chosen, err := j.resumeAttempt(attempts)
	if err != nil {
		return err
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		plan, ok := j.plan()
		if !ok {
			break
		}
		if j.log != nil {
			j.log.Debug("hls run planned",
				"entry", filepath.Base(j.dir), "from", j.cfg.From,
				"requested", j.requestedSegment(), "start", plan.Start, "end", plan.End)
		}
		before := j.count()
		chosen, err = j.runPlan(ctx, src, attempts, chosen, plan)
		j.rescan()
		if err != nil {
			if errors.Is(err, errRunSteered) && ctx.Err() == nil {
				j.clearSteer()
				continue
			}
			return err
		}
		if j.count() <= before {
			return fmt.Errorf("derive hls: run over segments %d-%d produced nothing", plan.Start, plan.End)
		}
	}
	return j.writePlaylist()
}

// runPlan performs one run. The first run of a job also chooses the rung of
// the ladder every later run uses: switching encoder mid-rendition would put
// two incompatible bitstreams under one init segment.
func (j *HLSJob) runPlan(ctx context.Context, src string, attempts []hlsAttempt, chosen int, plan segRange) (int, error) {
	if chosen >= 0 {
		return chosen, j.runAttempt(ctx, src, attempts[chosen], plan)
	}
	var (
		lastName string
		lastErr  error
		earlier  []string
	)
	for i, a := range attempts {
		if i > 0 {
			// The abandoned attempt's segments would otherwise be mixed into
			// the next one's rendition. The playlist goes with them and is
			// written again.
			if clearErr := clearDir(j.dir); clearErr != nil {
				return -1, fmt.Errorf("derive hls: %s failed (%w) and clearing the partial output failed: %w", lastName, lastErr, clearErr)
			}
			j.rescan()
			if err := j.writePlaylist(); err != nil {
				return -1, err
			}
		}
		err := j.runAttempt(ctx, src, a, plan)
		switch {
		case err == nil:
			return i, j.markEncoder(a.name)
		case errors.Is(err, errRunSteered):
			// The rung worked; it was cut short on purpose. Keep it.
			return i, errors.Join(j.markEncoder(a.name), err)
		case ctx.Err() != nil:
			// Cancelled or out of time: nothing to learn from another attempt,
			// and the next one would only be killed too.
			return -1, fmt.Errorf("derive hls: %s: %w", a.name, err)
		}
		if lastErr != nil {
			earlier = append(earlier, lastName+": "+lastErr.Error())
		}
		lastName, lastErr = a.name, err
		if i+1 < len(attempts) && j.log != nil {
			j.log.Warn("hls attempt failed, falling back",
				"entry", filepath.Base(j.dir), "attempt", a.name, "next", attempts[i+1].name, "err", err)
		}
	}
	if len(earlier) > 0 {
		return -1, fmt.Errorf("derive hls: %s failed: %w (earlier attempts: %s)", lastName, lastErr, strings.Join(earlier, "; "))
	}
	return -1, fmt.Errorf("derive hls: %s failed: %w", lastName, lastErr)
}

// runAttempt runs one ffmpeg over one stretch of the grid.
func (j *HLSJob) runAttempt(ctx context.Context, src string, a hlsAttempt, plan segRange) error {
	seg := plan
	if a.singleRun {
		// A stream copy cuts on the source's own keyframes, not on the 4 s
		// grid, so it can only ever produce the whole rendition at once.
		seg = segRange{Start: 0, End: j.total}
	}
	run := j.newRun(seg)
	before := j.snapshot()

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	j.beginRun(seg, cancel)
	stopWatch := j.watch(runCtx, run)
	err := runFFmpegIn(runCtx, j.cfg.FFmpegPath, j.dir, a.args(src, j.cfg.Height, run), j.log)
	stopWatch()
	steered := j.endRun()

	// Whatever the run left behind still has to be put on the timeline before
	// it counts as produced — including, for a run that was cut short, the
	// segments it did finish.
	publishErr := j.publishRun(run)
	j.tidyRun(run)
	j.rescan()
	switch {
	case err == nil && publishErr == nil:
		return nil
	case steered:
		return errRunSteered
	default:
		j.discardRun(before, run)
		if err == nil {
			return publishErr
		}
		return err
	}
}

// publishRun moves the run's finished segments onto the rendition's timeline
// and renames them into place. It is a no-op for a run that starts at zero,
// whose segments are already where the playlist says they are.
func (j *HLSJob) publishRun(run hlsRun) error {
	if run.startSeconds() == 0 {
		return nil
	}
	_, err := publishRawSegments(j.dir, run.initName, run.startSeconds())
	return err
}

// newRun describes one pass: where it sits on the grid, which init segment it
// writes and which playlist ffmpeg keeps its own notes in.
//
// Only the first run to reach the entry writes init.mp4. Every later run writes
// its own copy and tidyRun deletes it: the headers are byte-identical given
// identical encoder settings, so the copy carries no information, and
// overwriting the file a player may be part-way through downloading would hand
// it a truncated init segment — a dead stream for the price of nothing.
func (j *HLSJob) newRun(seg segRange) hlsRun {
	initName := HLSInitName
	if st, err := os.Stat(filepath.Join(j.dir, HLSInitName)); err == nil && st.Size() > 0 {
		initName = fmt.Sprintf("init-%05d.mp4", seg.Start)
	}
	return hlsRun{
		seg:      seg,
		total:    j.total,
		initName: initName,
		playlist: fmt.Sprintf("run-%05d.m3u8", seg.Start),
	}
}

// tidyRun removes what a finished run leaves lying about: the duplicate init
// segment and any `.tmp` the muxer was still holding.
func (j *HLSJob) tidyRun(run hlsRun) {
	if run.initName != HLSInitName {
		_ = os.Remove(filepath.Join(j.dir, run.initName))
	}
	entries, err := os.ReadDir(j.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		// A `.tmp` is the segment the muxer was still writing; a leftover
		// `.raw` is one it finished but that could not be put on the timeline.
		// Neither is part of the rendition.
		if strings.HasSuffix(e.Name(), ".tmp") || strings.HasSuffix(e.Name(), hlsRawSuffix) {
			_ = os.Remove(filepath.Join(j.dir, e.Name()))
		}
	}
}

// discardRun undoes a failed run: the segments it wrote are of unknown quality
// and may be from a rung that does not work here, so only what was on disk
// before it started is kept.
func (j *HLSJob) discardRun(before map[int]bool, run hlsRun) {
	_ = os.Remove(filepath.Join(j.dir, run.playlist))
	entries, err := os.ReadDir(j.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		i := HLSSegmentIndex(e.Name())
		if i >= 0 && !before[i] {
			_ = os.Remove(filepath.Join(j.dir, e.Name()))
		}
	}
	if !before[-1] {
		// -1 stands for "init.mp4 was already there"; see snapshot.
		_ = os.Remove(filepath.Join(j.dir, HLSInitName))
	}
}

// resumeAttempt decides which rung a job picking up a partial directory should
// use. A directory whose segments came from an encoder that is no longer on the
// ladder — the GPU was taken away, say — is started over rather than mixed.
func (j *HLSJob) resumeAttempt(attempts []hlsAttempt) (int, error) {
	if j.count() == 0 {
		return -1, nil
	}
	name := j.readEncoder()
	for i, a := range attempts {
		if a.name == name {
			if j.log != nil {
				j.log.Info("hls resuming a partial rendition",
					"entry", filepath.Base(j.dir), "encoder", name, "segments", j.count(), "of", j.total)
			}
			return i, nil
		}
	}
	if err := clearDir(j.dir); err != nil {
		return -1, err
	}
	j.rescan()
	return -1, j.writePlaylist()
}

func (j *HLSJob) markEncoder(name string) error {
	if err := os.WriteFile(filepath.Join(j.dir, hlsEncoderMarker), []byte(name), 0o600); err != nil {
		return fmt.Errorf("derive hls: record encoder: %w", err)
	}
	return nil
}

func (j *HLSJob) readEncoder() string {
	b, err := os.ReadFile(filepath.Join(j.dir, hlsEncoderMarker)) //nolint:gosec // cache dir plus a fixed name
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// watch keeps produced and the run position current while ffmpeg works, so
// hls_progress moves and a seek can tell how far ahead it is being asked to
// jump.
func (j *HLSJob) watch(ctx context.Context, run hlsRun) func() {
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		t := time.NewTicker(hlsWatchInterval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				// Publishing here rather than only at the end of the run is
				// what keeps a waiting player moving: a segment is no use to
				// it until it is on the timeline.
				if err := j.publishRun(run); err != nil && j.log != nil {
					j.log.Warn("hls publish", "entry", filepath.Base(j.dir), "err", err)
				}
				j.rescan()
			case <-ctx.Done():
				return
			case <-done:
				return
			}
		}
	}()
	return func() {
		close(done)
		<-stopped
	}
}

// rescan rebuilds the produced set from the directory. It is also how a job
// resuming a half-written entry after a restart finds out what is already
// there: a segment is produced when its file exists and is not empty, and the
// muxer's in-progress `.tmp` is not one.
func (j *HLSJob) rescan() {
	entries, err := os.ReadDir(j.dir)
	if err != nil {
		return
	}
	produced := make(map[int]bool, len(entries))
	for _, e := range entries {
		i := HLSSegmentIndex(e.Name())
		if i < 0 || i >= j.total {
			continue
		}
		info, err := e.Info()
		if err != nil || info.Size() == 0 {
			continue
		}
		produced[i] = true
	}
	j.mu.Lock()
	j.produced = produced
	pos := j.runStart
	for pos < j.runEnd && produced[pos] {
		pos++
	}
	j.runPos = pos
	j.mu.Unlock()
}

// snapshot copies the produced set, with -1 standing for "init.mp4 exists", so
// a failed run can be undone exactly.
func (j *HLSJob) snapshot() map[int]bool {
	j.mu.Lock()
	out := make(map[int]bool, len(j.produced)+1)
	for i := range j.produced {
		out[i] = true
	}
	j.mu.Unlock()
	if st, err := os.Stat(filepath.Join(j.dir, HLSInitName)); err == nil && st.Size() > 0 {
		out[-1] = true
	}
	return out
}

func (j *HLSJob) count() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.produced)
}

// requestedSegment is the segment a client last asked to be encoded first.
func (j *HLSJob) requestedSegment() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.requested
}

func (j *HLSJob) plan() (segRange, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return planNextRun(rangesFromSet(j.produced, j.total), j.requested, j.total)
}

func (j *HLSJob) beginRun(seg segRange, cancel context.CancelFunc) {
	j.mu.Lock()
	j.runStart, j.runEnd, j.runPos = seg.Start, seg.End, seg.Start
	j.runCancel, j.steered = cancel, false
	j.mu.Unlock()
}

// endRun clears the running-run state and reports whether the run was cut
// short by this job rather than by a failure.
func (j *HLSJob) endRun() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.runCancel = nil
	j.runStart, j.runEnd, j.runPos = 0, 0, 0
	return j.steered
}

func (j *HLSJob) clearSteer() {
	j.mu.Lock()
	j.steered = false
	j.mu.Unlock()
}

func (j *HLSJob) writePlaylist() error {
	return writeHLSPlaylist(j.dir, j.total, j.duration, readRunDurations(j.dir))
}

// hlsRun is one ffmpeg pass over a contiguous stretch of the segment grid.
type hlsRun struct {
	seg      segRange
	total    int
	initName string
	playlist string
}

// startSeconds is where on the timeline the run begins. Always a multiple of
// the segment length, which is what lets -force_key_frames put a keyframe on
// the shared grid.
func (r hlsRun) startSeconds() int { return r.seg.Start * hlsSegmentSeconds }

// segmentPattern is what ffmpeg writes segments as. A run that starts part-way
// through the video writes them under a name the route will not serve, because
// their timestamps still have to be moved onto the rendition's timeline before
// a player may see them; see hlsrebase.go.
func (r hlsRun) segmentPattern() string {
	if r.startSeconds() > 0 {
		return "seg%05d.m4s" + hlsRawSuffix
	}
	return "seg%05d.m4s"
}

// durationSeconds is how much of the timeline the run covers, or 0 when it runs
// to the end of the video and ffmpeg needs no -t at all.
func (r hlsRun) durationSeconds() int {
	if r.seg.End >= r.total {
		return 0
	}
	return r.seg.Len() * hlsSegmentSeconds
}

// HLSSegmentName is the file (and playlist URI) of segment i.
func HLSSegmentName(i int) string { return hlsSegmentName(i) }

// HLSSegmentIndex is the segment index a file name refers to, or -1 when the
// name is not a segment. Routes use it to tell which segment a request is
// waiting for.
func HLSSegmentIndex(name string) int { return hlsSegmentIndex(name) }

// HLSSegmentIndexAt is the segment a playback position falls in.
func HLSSegmentIndexAt(seconds float64) int {
	if seconds <= 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return 0
	}
	return int(seconds) / hlsSegmentSeconds
}

// HLSRegistry publishes running HLS jobs by cache entry name, so a segment
// request can find the job it is waiting on and steer it. Jobs are added when
// they are prepared and removed when they end; a nil registry is inert.
type HLSRegistry struct {
	mu   sync.Mutex
	jobs map[string]*HLSJob
}

func NewHLSRegistry() *HLSRegistry { return &HLSRegistry{jobs: map[string]*HLSJob{}} }

// Get returns the running job for a cache entry, or nil.
func (r *HLSRegistry) Get(name string) *HLSJob {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.jobs[name]
}

func (r *HLSRegistry) put(name string, j *HLSJob) func() {
	if r == nil {
		return func() {}
	}
	r.mu.Lock()
	r.jobs[name] = j
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		if r.jobs[name] == j {
			delete(r.jobs, name)
		}
		r.mu.Unlock()
	}
}

// probeDuration asks ffprobe how long the source is, for the rare video whose
// duration TA did not parse. It reads through a loopback source of its own, so
// the token stays out of ffprobe's argv exactly as it stays out of ffmpeg's.
func probeDuration(ctx context.Context, ffmpegPath string, open RangeSourceFunc, log *slog.Logger) (float64, error) {
	lb, err := newLoopbackSource(log)
	if err != nil {
		return 0, err
	}
	defer lb.close()
	src, release := lb.register(open)
	defer release()

	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	//nolint:gosec // G204: binary derived from the configured ffmpeg path, args generated here
	out, err := exec.CommandContext(ctx, ffprobePath(ffmpegPath),
		"-hide_banner", "-loglevel", "error",
		"-show_entries", "format=duration", "-of", "default=nw=1:nk=1", src).Output()
	if err != nil {
		return 0, fmt.Errorf("ffprobe: %w", err)
	}
	d, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("ffprobe reported no usable duration (%q)", strings.TrimSpace(string(out)))
	}
	return d, nil
}

// ffprobePath is the ffprobe next to the configured ffmpeg. The two ship
// together, so an operator who set FFMPEG_PATH does not have to set a second
// variable for its sibling.
func ffprobePath(ffmpegPath string) string {
	dir, base := filepath.Split(ffmpegPath)
	if rest, ok := strings.CutSuffix(base, "ffmpeg"); ok {
		return dir + rest + "ffprobe"
	}
	return filepath.Join(dir, "ffprobe")
}
