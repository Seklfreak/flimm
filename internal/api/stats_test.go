package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Seklfreak/flimm/internal/db/sqlc"
	"github.com/Seklfreak/flimm/internal/sqlctest"
	"github.com/Seklfreak/flimm/internal/ta"
)

// statsQuerier records what the handler asked for and answers with fixtures.
type statsQuerier struct {
	asked []uuid.UUID
	since []pgtype.Timestamptz
	zones []string
}

func (s *statsQuerier) querier() *sqlctest.FakeQuerier {
	return &sqlctest.FakeQuerier{
		WatchTotalsFn: func(_ context.Context, arg sqlc.WatchTotalsParams) (sqlc.WatchTotalsRow, error) {
			s.asked = append(s.asked, arg.UserID)
			s.since = append(s.since, arg.Since)
			return sqlc.WatchTotalsRow{
				Started: 412, Finished: 297, Seconds: 987_654,
				Since: pgtype.Timestamptz{Time: time.Date(2026, 1, 2, 15, 4, 0, 0, time.UTC), Valid: true},
			}, nil
		},
		WatchTopChannelsFn: func(_ context.Context, arg sqlc.WatchTopChannelsParams) ([]sqlc.WatchTopChannelsRow, error) {
			s.asked = append(s.asked, arg.UserID)
			return []sqlc.WatchTopChannelsRow{
				{ChannelID: "UC1", ChannelName: "Slow Kitchen", Videos: 40, Seconds: 120_000},
				{ChannelID: "UC2", ChannelName: "Field Tapes", Videos: 12, Seconds: 4_000},
			}, nil
		},
		WatchByHourFn: func(_ context.Context, arg sqlc.WatchByHourParams) ([]sqlc.WatchByHourRow, error) {
			s.asked = append(s.asked, arg.UserID)
			s.zones = append(s.zones, arg.Zone)
			return []sqlc.WatchByHourRow{{Hour: 0, Videos: 3}, {Hour: 21, Videos: 40}}, nil
		},
		WatchByWeekdayFn: func(_ context.Context, arg sqlc.WatchByWeekdayParams) ([]sqlc.WatchByWeekdayRow, error) {
			s.asked = append(s.asked, arg.UserID)
			// ISODOW: 1 is Monday, 7 is Sunday.
			return []sqlc.WatchByWeekdayRow{{Weekday: 1, Videos: 5}, {Weekday: 7, Videos: 9}}, nil
		},
		WatchByMonthFn: func(_ context.Context, arg sqlc.WatchByMonthParams) ([]sqlc.WatchByMonthRow, error) {
			s.asked = append(s.asked, arg.UserID)
			return []sqlc.WatchByMonthRow{{Month: "2026-07", Videos: 30, Seconds: 60_000}}, nil
		},
	}
}

func statsServer(t *testing.T, q *statsQuerier) http.Handler {
	t.Helper()
	return NewServer(Options{
		Querier:     q.querier(),
		TA:          ta.NewFake(),
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		AppName:     "Flimm",
		MediaSecret: testSecret,
	}).Router()
}

func TestStatsSummarisesAViewersHistory(t *testing.T) {
	rec := do(t, statsServer(t, &statsQuerier{}), http.MethodGet, "/api/v1/stats", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	got := decode[WatchStats](t, rec)

	if got.Started != 412 || got.Finished != 297 || got.Seconds != 987_654 {
		t.Errorf("totals = %+v", got)
	}
	if got.Since == nil || got.Since.Year() != 2026 {
		t.Errorf("since = %v", got.Since)
	}
	if len(got.TopChannels) != 2 || got.TopChannels[0].Name != "Slow Kitchen" {
		t.Errorf("channels = %+v", got.TopChannels)
	}
	// Always full-length, so a client draws the shape without deciding what a
	// missing hour means.
	if len(got.ByHour) != 24 || len(got.ByWeekday) != 7 {
		t.Fatalf("by_hour = %d, by_weekday = %d", len(got.ByHour), len(got.ByWeekday))
	}
	if got.ByHour[0] != 3 || got.ByHour[21] != 40 || got.ByHour[5] != 0 {
		t.Errorf("by_hour = %v", got.ByHour)
	}
	// ISODOW 1..7 becomes a Monday-first array.
	if got.ByWeekday[0] != 5 || got.ByWeekday[6] != 9 {
		t.Errorf("by_weekday = %v", got.ByWeekday)
	}
	if got.Range != "all" || got.Zone != "UTC" {
		t.Errorf("range = %q, zone = %q", got.Range, got.Zone)
	}
}

// The breakdowns are about the viewer's evenings, not the server's.
func TestStatsUsesTheClientsTimezone(t *testing.T) {
	q := &statsQuerier{}
	rec := do(t, statsServer(t, q), http.MethodGet, "/api/v1/stats?tz=America/New_York", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if len(q.zones) == 0 || q.zones[0] != "America/New_York" {
		t.Errorf("zones = %v", q.zones)
	}
	if got := decode[WatchStats](t, rec).Zone; got != "America/New_York" {
		t.Errorf("zone = %q", got)
	}
	// A zone that does not exist falls back rather than failing: a stats page
	// is not worth a 500 over a bad string.
	rec = do(t, statsServer(t, &statsQuerier{}), http.MethodGet, "/api/v1/stats?tz=Mars/Olympus", "")
	if rec.Code != http.StatusOK || decode[WatchStats](t, rec).Zone != "UTC" {
		t.Errorf("unknown zone: status %d", rec.Code)
	}
}

func TestStatsRangesAreCalendarWindows(t *testing.T) {
	zone := time.UTC
	now := time.Now().In(zone)

	name, since := statsRange("year", zone)
	if name != "year" || !since.Valid || since.Time.Month() != time.January || since.Time.Day() != 1 {
		t.Errorf("year = %v %v", name, since.Time)
	}
	if since.Time.Year() != now.Year() {
		t.Errorf("year cutoff = %v, want this year", since.Time)
	}

	name, since = statsRange("month", zone)
	if name != "month" || since.Time.Day() != 1 || since.Time.Month() != now.Month() {
		t.Errorf("month = %v %v", name, since.Time)
	}

	// Everything: no cutoff at all, which the queries read as NULL.
	if name, since = statsRange("", zone); name != "all" || since.Valid {
		t.Errorf("all = %v %+v", name, since)
	}
}

// A filter that silently ignores you is worse than one that says no.
func TestStatsRefusesAnUnknownRange(t *testing.T) {
	rec := do(t, statsServer(t, &statsQuerier{}), http.MethodGet, "/api/v1/stats?range=fortnight", "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// Every query is scoped to the caller. Stats are the whole of someone's
// viewing, so a leak here would be the worst kind.
func TestStatsAreScopedToTheCurrentUser(t *testing.T) {
	q := &statsQuerier{}
	rec := do(t, statsServer(t, q), http.MethodGet, "/api/v1/stats", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if len(q.asked) != 5 {
		t.Fatalf("asked %d queries, want one per breakdown", len(q.asked))
	}
	for _, id := range q.asked {
		if id != q.asked[0] {
			t.Fatalf("queries used different user ids: %v", q.asked)
		}
		if id == uuid.Nil {
			t.Fatal("a query ran with no user id at all")
		}
	}
}

// The monthly bars end at the current month and start eleven months earlier,
// so a year of history is a year of bars.
func TestTheMonthlyWindowIsTwelveMonths(t *testing.T) {
	zone := time.UTC
	start := monthsAgo(statsMonths, zone)
	now := time.Now().In(zone)
	if start.Day() != 1 {
		t.Errorf("start = %v, want the first of a month", start)
	}
	months := (now.Year()-start.Year())*12 + int(now.Month()) - int(start.Month())
	if months != statsMonths-1 {
		t.Errorf("window spans %d months, want %d", months+1, statsMonths)
	}
}
