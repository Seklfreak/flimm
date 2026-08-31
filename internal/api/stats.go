package api

import (
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Seklfreak/flimm/internal/db/sqlc"
)

// Watch stats: what a viewer's history adds up to.
//
// Everything here comes out of `watch_events`, which holds **one row per video
// per viewer** — the furthest point reached in it, whether it was finished, and
// when it was first and last played. That shape decides what can honestly be
// said and what cannot:
//
//   - "Watched" is the sum of those furthest points: a finished video counts
//     its whole duration, an abandoned one counts where it stopped. A video
//     watched three times counts once, and a video skipped through counts the
//     part that was skipped. It is a floor on time spent, not a stopwatch.
//   - "When" is when a video was *first started*, because that is the only
//     moment the table records exactly. A viewer who starts at midnight and
//     watches till two shows up at midnight.
//
// Saying that plainly is the point: an invented number here would be
// indistinguishable from a real one.

const (
	// statsTopChannels is how many channels the breakdown names. Enough to
	// recognise a habit, few enough to read on a phone.
	statsTopChannels = 8
	// statsMonths is how far back the monthly bars go.
	statsMonths = 12
)

// WatchStats is what `GET /stats` answers.
type WatchStats struct {
	// Started is videos with any watch history; Finished is those the server
	// marked watched.
	Started  int64 `json:"started"`
	Finished int64 `json:"finished"`
	// Seconds is the summed furthest point reached — see the note above.
	Seconds float64 `json:"seconds"`
	// Since is the first play recorded for this viewer, or null when there is
	// no history at all.
	Since *time.Time `json:"since"`
	// Range is what the numbers cover: "all", "year" or "month".
	Range string `json:"range"`
	// Zone is the timezone the hour, weekday and month breakdowns were
	// computed in — the one the client asked for.
	Zone string `json:"zone"`

	TopChannels []StatsChannel `json:"top_channels"`
	// ByHour is 24 counts, midnight first; ByWeekday is 7, Monday first. Both
	// are always full-length, so a client can draw the shape without deciding
	// what a missing hour means.
	ByHour    []int64      `json:"by_hour"`
	ByWeekday []int64      `json:"by_weekday"`
	ByMonth   []StatsMonth `json:"by_month"`
}

// StatsChannel is one channel's share.
type StatsChannel struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Videos  int64   `json:"videos"`
	Seconds float64 `json:"seconds"`
}

// StatsMonth is one calendar month, `YYYY-MM` in the requested zone.
type StatsMonth struct {
	Month   string  `json:"month"`
	Videos  int64   `json:"videos"`
	Seconds float64 `json:"seconds"`
}

// getStats answers GET /stats.
func (s *Server) getStats(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	if !validStatsRange(r.URL.Query().Get("range")) {
		writeError(w, http.StatusBadRequest, "invalid range")
		return
	}
	zone := statsZone(r.URL.Query().Get("tz"))
	rangeName, since := statsRange(r.URL.Query().Get("range"), zone)

	totals, err := s.q.WatchTotals(r.Context(), sqlc.WatchTotalsParams{UserID: uid, Since: since})
	if err != nil {
		s.writeDBError(w, "watch totals", err)
		return
	}
	channels, err := s.q.WatchTopChannels(r.Context(), sqlc.WatchTopChannelsParams{
		UserID: uid, Since: since, RowLimit: statsTopChannels,
	})
	if err != nil {
		s.writeDBError(w, "watch channels", err)
		return
	}
	hours, err := s.q.WatchByHour(r.Context(), sqlc.WatchByHourParams{UserID: uid, Since: since, Zone: zone.String()})
	if err != nil {
		s.writeDBError(w, "watch by hour", err)
		return
	}
	weekdays, err := s.q.WatchByWeekday(r.Context(), sqlc.WatchByWeekdayParams{UserID: uid, Since: since, Zone: zone.String()})
	if err != nil {
		s.writeDBError(w, "watch by weekday", err)
		return
	}
	months, err := s.q.WatchByMonth(r.Context(), sqlc.WatchByMonthParams{
		UserID: uid, Zone: zone.String(),
		Since: pgtype.Timestamptz{Time: monthsAgo(statsMonths, zone), Valid: true},
	})
	if err != nil {
		s.writeDBError(w, "watch by month", err)
		return
	}

	out := WatchStats{
		Started:     totals.Started,
		Finished:    totals.Finished,
		Seconds:     totals.Seconds,
		Range:       rangeName,
		Zone:        zone.String(),
		TopChannels: make([]StatsChannel, 0, len(channels)),
		ByHour:      make([]int64, 24),
		ByWeekday:   make([]int64, 7),
		ByMonth:     make([]StatsMonth, 0, len(months)),
	}
	if totals.Since.Valid {
		at := totals.Since.Time.UTC()
		out.Since = &at
	}
	for _, c := range channels {
		out.TopChannels = append(out.TopChannels, StatsChannel{
			ID: c.ChannelID, Name: c.ChannelName, Videos: c.Videos, Seconds: c.Seconds,
		})
	}
	for _, h := range hours {
		if h.Hour >= 0 && int(h.Hour) < len(out.ByHour) {
			out.ByHour[h.Hour] = h.Videos
		}
	}
	for _, d := range weekdays {
		// ISODOW is 1..7 with Monday first, which is the order clients draw.
		if d.Weekday >= 1 && int(d.Weekday) <= len(out.ByWeekday) {
			out.ByWeekday[d.Weekday-1] = d.Videos
		}
	}
	for _, m := range months {
		out.ByMonth = append(out.ByMonth, StatsMonth{Month: m.Month, Videos: m.Videos, Seconds: m.Seconds})
	}
	writeJSON(w, http.StatusOK, out)
}

// statsZone reads the client's timezone. An hour-of-day breakdown computed in
// the server's zone would be wrong for everyone not sitting in it, and there is
// no reason for the server to guess: the client knows.
func statsZone(name string) *time.Location {
	if name == "" {
		return time.UTC
	}
	zone, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return zone
}

// statsRange turns `?range=` into a cutoff. "year" and "month" are calendar
// windows in the viewer's own zone, not rolling ones: "this year" should mean
// the year on the wall.
func statsRange(name string, zone *time.Location) (string, pgtype.Timestamptz) {
	now := time.Now().In(zone)
	switch name {
	case "year":
		start := time.Date(now.Year(), 1, 1, 0, 0, 0, 0, zone)
		return "year", pgtype.Timestamptz{Time: start, Valid: true}
	case "month":
		start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, zone)
		return "month", pgtype.Timestamptz{Time: start, Valid: true}
	default:
		// NULL, which the queries read as "no cutoff".
		return "all", pgtype.Timestamptz{}
	}
}

// monthsAgo is the first day of the month `n` months back, in `zone`.
func monthsAgo(n int, zone *time.Location) time.Time {
	now := time.Now().In(zone)
	first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, zone)
	return first.AddDate(0, -(n - 1), 0)
}

// validStatsRange is the set a client may ask for. An unknown one is refused
// rather than quietly answered with everything, which would read as a working
// filter that ignores you.
func validStatsRange(name string) bool {
	switch name {
	case "", "all", "year", "month":
		return true
	}
	return false
}
