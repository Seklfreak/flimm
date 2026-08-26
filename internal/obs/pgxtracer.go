package obs

import (
	"context"

	"github.com/getsentry/sentry-go"
	"github.com/jackc/pgx/v5"
)

// PgxTracer records each query as a Sentry span so DB time shows up inside
// request transactions, with the SQL (including sqlc's "-- name:" comment)
// as the span description. Queries with no surrounding trace — startup,
// health checks, anything outside a sampled request — record nothing.
type PgxTracer struct{}

type pgxSpanKey struct{}

func (PgxTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	parent := sentry.SpanFromContext(ctx)
	if parent == nil {
		return ctx
	}
	span := parent.StartChild("db.sql.query", sentry.WithDescription(data.SQL))
	return context.WithValue(ctx, pgxSpanKey{}, span)
}

func (PgxTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	span, ok := ctx.Value(pgxSpanKey{}).(*sentry.Span)
	if !ok {
		return
	}
	if data.Err != nil {
		span.Status = sentry.SpanStatusInternalError
	} else {
		span.Status = sentry.SpanStatusOK
	}
	span.Finish()
}
