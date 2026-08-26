package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Seklfreak/archive-client/internal/db/sqlc"
	"github.com/Seklfreak/archive-client/internal/sqlctest"
	"github.com/Seklfreak/archive-client/internal/ta"
)

const testSecret = "test-secret"

// eventStore is an in-memory watch_events table wired into a FakeQuerier so
// progress/history tests can observe writes. The mutex is required, not
// defensive: markAllSeen fans its writes out across an errgroup, so the
// querier callbacks below run concurrently.
type eventStore struct {
	mu     sync.Mutex
	events map[string]sqlc.WatchEvent
}

func newEventStore() *eventStore { return &eventStore{events: map[string]sqlc.WatchEvent{}} }

func (es *eventStore) querier() *sqlctest.FakeQuerier {
	return &sqlctest.FakeQuerier{
		ListWatchEventsForVideosFn: func(_ context.Context, arg sqlc.ListWatchEventsForVideosParams) ([]sqlc.WatchEvent, error) {
			es.mu.Lock()
			defer es.mu.Unlock()
			var out []sqlc.WatchEvent
			for _, id := range arg.VideoIds {
				if ev, ok := es.events[id]; ok {
					out = append(out, ev)
				}
			}
			return out, nil
		},
		GetWatchEventFn: func(_ context.Context, arg sqlc.GetWatchEventParams) (sqlc.WatchEvent, error) {
			es.mu.Lock()
			defer es.mu.Unlock()
			ev, ok := es.events[arg.VideoID]
			if !ok {
				return sqlc.WatchEvent{}, pgx.ErrNoRows
			}
			return ev, nil
		},
		UpsertProgressFn: func(_ context.Context, arg sqlc.UpsertProgressParams) (sqlc.WatchEvent, error) {
			es.mu.Lock()
			defer es.mu.Unlock()
			now := pgtype.Timestamptz{Time: time.Now(), Valid: true}
			ev, ok := es.events[arg.VideoID]
			if !ok {
				ev = sqlc.WatchEvent{ID: uuid.New(), UserID: arg.UserID, VideoID: arg.VideoID, FirstPlayedAt: now}
			}
			ev.Position, ev.Duration, ev.Title, ev.ChannelID, ev.ChannelName = arg.Position, arg.Duration, arg.Title, arg.ChannelID, arg.ChannelName
			ev.LastPlayedAt, ev.Hidden = now, false
			if arg.Completed && !ev.CompletedAt.Valid {
				ev.CompletedAt = now
			}
			es.events[arg.VideoID] = ev
			return ev, nil
		},
		SetWatchedFn: func(_ context.Context, arg sqlc.SetWatchedParams) (sqlc.WatchEvent, error) {
			es.mu.Lock()
			defer es.mu.Unlock()
			now := pgtype.Timestamptz{Time: time.Now(), Valid: true}
			ev, ok := es.events[arg.VideoID]
			if !ok {
				ev = sqlc.WatchEvent{ID: uuid.New(), UserID: arg.UserID, VideoID: arg.VideoID, FirstPlayedAt: now, LastPlayedAt: now, Duration: arg.Duration}
			}
			if arg.Watched {
				if !ev.CompletedAt.Valid {
					ev.CompletedAt = now
				}
			} else {
				ev.CompletedAt = pgtype.Timestamptz{}
				ev.Position = 0
			}
			es.events[arg.VideoID] = ev
			return ev, nil
		},
		ResetPositionFn: func(_ context.Context, arg sqlc.ResetPositionParams) error {
			es.mu.Lock()
			defer es.mu.Unlock()
			if ev, ok := es.events[arg.VideoID]; ok {
				ev.Position = 0
				es.events[arg.VideoID] = ev
			}
			return nil
		},
		ListInProgressFn: func(context.Context, sqlc.ListInProgressParams) ([]sqlc.WatchEvent, error) {
			es.mu.Lock()
			defer es.mu.Unlock()
			var out []sqlc.WatchEvent
			for _, ev := range es.events {
				if !ev.CompletedAt.Valid && ev.Position > 0 && !ev.Hidden {
					out = append(out, ev)
				}
			}
			return out, nil
		},
		GetPrefsFn: func(context.Context, uuid.UUID) ([]byte, error) { return nil, pgx.ErrNoRows },
		ListFeedChannelsForUserFn: func(context.Context, uuid.UUID) ([]sqlc.ListFeedChannelsForUserRow, error) {
			return nil, nil
		},
	}
}

func newTestServer(client ta.Client, q sqlc.Querier) *Server {
	return NewServer(Options{
		Querier:     q,
		TA:          client,
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		AppName:     "Archive",
		MediaSecret: testSecret,
	})
}

func do(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return out
}

func video(id, channel string, published string, duration float64, watched bool) ta.Video {
	return ta.Video{
		YoutubeID: id, Title: "Video " + id, Published: published,
		Channel:  ta.Channel{ChannelID: channel, ChannelName: "Channel " + channel},
		Player:   ta.Player{Duration: duration, Watched: watched},
		MediaURL: channel + "/" + id + ".mp4",
		VidType:  "videos",
	}
}

func ids(items []VideoSummary) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.ID)
	}
	return out
}
