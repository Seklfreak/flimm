package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/sync/errgroup"

	"github.com/Seklfreak/flimm/internal/db/sqlc"
	"github.com/Seklfreak/flimm/internal/ta"
)

const (
	defaultPageSize = 30
	maxPageSize     = 100
	// fanoutLimit bounds concurrent TA requests per API call.
	fanoutLimit = 8
	// maxListVideos caps how many videos are pulled per channel (or for the
	// everything feed) when building a merged list. Beyond this, older
	// videos are unreachable through the feed — use search or the channel.
	maxListVideos = 500
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// writeTAError maps a TA client error: 404 for unknown resources (never
// leaking existence), 502 when TA is down, 500 otherwise.
func (s *Server) writeTAError(w http.ResponseWriter, what string, err error) {
	switch {
	case errors.Is(err, ta.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, ta.ErrUnavailable):
		s.log.Error(what, "err", err)
		writeError(w, http.StatusBadGateway, "tubearchivist unavailable")
	case errors.Is(err, context.Canceled):
		// client went away; nothing to report
	default:
		s.log.Error(what, "err", err)
		writeError(w, http.StatusInternalServerError, what+" failed")
	}
}

// writeDBError logs and answers 500 for storage failures; a pgx.ErrNoRows
// becomes 404.
func (s *Server) writeDBError(w http.ResponseWriter, what string, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	s.log.Error(what, "err", err)
	writeError(w, http.StatusInternalServerError, what+" failed")
}

// decodeBody reads a JSON body of at most 1 MB.
func decodeBody(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v)
}

// Page is the common list envelope.
type Page[T any] struct {
	Items    []T   `json:"items"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
	Total    int64 `json:"total"`
}

type paging struct {
	Page, Size int
}

func (p paging) offset() int { return p.Page * p.Size }

// parsePaging reads page (0-based) and page_size (default 30, max 100).
func parsePaging(r *http.Request) paging {
	p := paging{Size: defaultPageSize}
	if v, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && v >= 0 {
		p.Page = v
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("page_size")); err == nil && v > 0 {
		p.Size = min(v, maxPageSize)
	}
	return p
}

// slicePage returns the requested window of items as a Page.
func slicePage[T any](items []T, p paging) Page[T] {
	from := min(p.offset(), len(items))
	to := min(from+p.Size, len(items))
	out := items[from:to]
	if out == nil {
		out = []T{}
	}
	return Page[T]{Items: out, Page: p.Page, PageSize: p.Size, Total: int64(len(items))}
}

// parallel runs fn over items with at most fanoutLimit in flight; the first
// error cancels the rest.
func parallel[T any](ctx context.Context, items []T, fn func(ctx context.Context, i int, item T) error) error {
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(fanoutLimit)
	for i, it := range items {
		g.Go(func() error { return fn(ctx, i, it) })
	}
	return g.Wait()
}

func tsPtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	u := t.Time.UTC()
	return &u
}

func ts(t pgtype.Timestamptz) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time.UTC()
}

// withTx runs fn against a transactional Querier when a pool is configured,
// or the plain Querier otherwise (tests with a FakeQuerier).
func (s *Server) withTx(ctx context.Context, fn func(q sqlc.Querier) error) error {
	if s.pool == nil {
		return fn(s.q)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := fn(sqlc.New(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
