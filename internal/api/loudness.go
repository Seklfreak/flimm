package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Seklfreak/flimm/internal/media"
)

// Loudness normalisation, from the API's side.
//
// The server measures and decides: a client is handed a number of decibels and
// applies it, and every client applies the same one. Deciding it here is what
// keeps a video from sounding different on the TV and the phone, and it is
// what makes the target a deployment's property rather than four clients'.
//
// The measurement is a real decode of the audio, so the endpoint never blocks
// on it: the first call starts the pass and says `running`, and the answer
// turns up on a later call — the same shape as a transcode, and as the scrub
// previews.

func loudnessName(id string) string { return media.LoudnessVariant + "-" + id }

// getVideoLoudness answers GET /videos/{id}/loudness.
func (s *Server) getVideoLoudness(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validMediaID.MatchString(id) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if s.mediaCache == nil {
		writeError(w, http.StatusServiceUnavailable, "derived media is not configured")
		return
	}
	name := loudnessName(id)
	// A finished measurement is answered without touching the archive at all.
	if l, ok := media.ReadLoudness(s.mediaCache.Dir(name)); ok {
		s.mediaCache.TouchDir(name)
		writeJSON(w, http.StatusOK, loudnessResponse(l, media.StateDone))
		return
	}
	v, err := s.ta.GetVideo(r.Context(), id)
	if err != nil {
		s.writeTAError(w, "get video", err)
		return
	}
	if v.MediaURL == "" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	state := s.mediaCache.StartScan(name,
		media.Measure(s.ffmpegPath, s.log, s.rangeSource(taMediaPath(v.MediaURL))))
	// Nothing to apply yet. A gain of 0 is the honest answer while the pass
	// runs and after one that failed alike: play the video as it was archived.
	writeJSON(w, http.StatusOK, LoudnessInfo{
		State:      string(state),
		TargetLUFS: media.TargetLUFS,
	})
}

func loudnessResponse(l media.Loudness, state media.JobState) LoudnessInfo {
	return LoudnessInfo{
		State:        string(state),
		GainDB:       l.GainDB,
		TargetLUFS:   l.TargetLUFS,
		MeasuredLUFS: l.MeasuredLUFS,
		PeakDBTP:     l.PeakDBTP,
		RangeLU:      l.RangeLU,
	}
}
