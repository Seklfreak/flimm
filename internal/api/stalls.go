package api

import (
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Seklfreak/flimm/internal/media"
)

// Where playback stalls are reported, and why they are reported *here*.
//
// A client is the only side that knows the picture stopped: no request fails,
// nothing errors, the viewer simply watches a spinner. The server is the only
// side that knows why it might have — where the encoder had got to, whether the
// segment being waited for existed yet, how long its own reads took. Neither
// half is worth much alone, which is why the client says "I stalled at 41:12 of
// this rendition for 3 seconds" and the server answers the question the viewer
// actually has: was that us making it, or us sending it?

const (
	// stallsKept is how many recent stalls `/healthz` shows an admin. Enough
	// to see a pattern in an evening's watching, small enough to hold.
	stallsKept = 50
	// stallMinSeconds ignores the sub-second gaps every player has between
	// segments. A stall worth a log line is one a person noticed.
	stallMinSeconds = 0.4
)

// StallReport is what a client sends: what it was playing and how long the
// picture stopped for.
type StallReport struct {
	// Position in the video, in seconds, where playback stopped.
	Position float64 `json:"position"`
	// Seconds the stall lasted.
	Seconds float64 `json:"seconds"`
	// Height of the compatible rendition being played, or 0 for the archived
	// file itself.
	Height int `json:"height"`
	// Client names the platform, for telling a Wi-Fi Apple TV apart from a
	// browser on the same LAN.
	Client string `json:"client"`
}

// Stall is one attributed stall, as `/healthz` shows it.
type Stall struct {
	At       time.Time `json:"at"`
	VideoID  string    `json:"video_id"`
	Position float64   `json:"position"`
	Seconds  float64   `json:"seconds"`
	Height   int       `json:"height"`
	Client   string    `json:"client"`
	// Reason is the server's attribution; see stallReason.
	Reason string `json:"reason"`
	// Segment is the segment that position falls in, and Encoder where the
	// run had got to — the two numbers the reason is drawn from.
	Segment int `json:"segment"`
	Encoder int `json:"encoder"`
}

// The reasons a stall gets. Deliberately few: each one points at a different
// thing to go and look at.
const (
	// stallEncoder — the segment did not exist yet. The transcode is behind
	// the viewer, which is the one cause the server can fix.
	stallEncoder = "encoder_behind"
	// stallDelivery — the segment existed. Whatever took the time was between
	// the disk and the screen: the network, the client's buffer, the decoder.
	stallDelivery = "delivery"
	// stallSource — the archived file is being played directly, so no
	// rendition is involved and TubeArchivist or the network served it.
	stallSource = "source"
	// stallUnknown — nothing left to ask: no run, and no segment on disk
	// either, usually because the rendition was evicted since.
	stallUnknown = "unknown"
)

// stallLog keeps the recent ones for /healthz.
type stallLog struct {
	mu     sync.Mutex
	recent []Stall
}

func (l *stallLog) add(s Stall) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.recent = append(l.recent, s)
	if len(l.recent) > stallsKept {
		l.recent = l.recent[len(l.recent)-stallsKept:]
	}
}

func (l *stallLog) list() []Stall {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Stall, len(l.recent))
	copy(out, l.recent)
	return out
}

// postStall answers POST /videos/{id}/stall.
func (s *Server) postStall(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var report StallReport
	if err := decodeBody(r, &report); err != nil {
		writeError(w, http.StatusBadRequest, "invalid stall report")
		return
	}
	if report.Seconds < stallMinSeconds {
		// Not a stall, just the gap between two segments.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	stall := s.attributeStall(id, report)
	s.stalls.add(stall)
	s.log.Info("playback stalled",
		"video", stall.VideoID,
		"reason", stall.Reason,
		"seconds", stall.Seconds,
		"position", stall.Position,
		"height", stall.Height,
		"segment", stall.Segment,
		"encoder", stall.Encoder,
		"client", stall.Client,
	)
	w.WriteHeader(http.StatusNoContent)
}

// attributeStall says why, from what the server knows about that rendition at
// that position.
//
// It is deliberately a *claim about the segment*, not a guess about the
// network: either the bytes existed when the viewer wanted them or they did
// not, and that single fact decides which half of the system to go and look at.
func (s *Server) attributeStall(id string, report StallReport) Stall {
	stall := Stall{
		At:       time.Now().UTC(),
		VideoID:  id,
		Position: report.Position,
		Seconds:  report.Seconds,
		Height:   report.Height,
		Client:   report.Client,
		Segment:  -1,
		Encoder:  -1,
		Reason:   stallSource,
	}
	if report.Height == 0 {
		return stall
	}
	name := media.HLSName(id, report.Height)
	stall.Segment = media.HLSSegmentIndexAt(report.Position)
	job := s.hlsJobs.Get(name)
	if job == nil {
		// No run to ask, which is the ordinary case for a rendition that was
		// finished before this playback started. The segment file itself
		// answers just as well, and more certainly: it is either on disk or it
		// is not.
		stall.Reason = s.stallReasonOnDisk(id, name, stall.Segment)
		return stall
	}
	stall.Encoder = job.RunPosition()
	if job.Has(stall.Segment) {
		stall.Reason = stallDelivery
	} else {
		stall.Reason = stallEncoder
	}
	return stall
}

// stallReasonOnDisk attributes a stall in a rendition nothing is encoding.
//
// The id reaches this handler unverified — a stall report is never checked
// against TubeArchivist — so it goes through the same gate as every other path
// built from one before it names a file.
func (s *Server) stallReasonOnDisk(id, name string, segment int) string {
	if s.mediaCache == nil || !validMediaID.MatchString(id) {
		return stallUnknown
	}
	path := filepath.Join(s.mediaCache.Dir(name), media.HLSSegmentName(segment))
	if _, err := os.Stat(path); err != nil { //nolint:gosec // cache dir + a validated id + a generated name
		// Either the rendition was evicted between playing and reporting, or
		// it never held that segment. Neither says anything about the stall.
		return stallUnknown
	}
	return stallDelivery
}
