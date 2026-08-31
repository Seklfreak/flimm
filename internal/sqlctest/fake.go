// Package sqlctest provides a fake sqlc.Querier for tests. It embeds the
// interface, so only the methods a test needs are set; any unset method panics
// if called (a clear signal the test exercised an unexpected query).
package sqlctest

import (
	"context"

	"github.com/google/uuid"

	"github.com/Seklfreak/flimm/internal/db/sqlc"
)

type FakeQuerier struct {
	sqlc.Querier // embedded; unset methods panic on call

	AddFeedChannelFn             func(context.Context, sqlc.AddFeedChannelParams) error
	CountHistoryFn               func(context.Context, sqlc.CountHistoryParams) (int64, error)
	CreateFeedFn                 func(context.Context, sqlc.CreateFeedParams) (sqlc.Feed, error)
	DeleteChannelFromUserFeedsFn func(context.Context, sqlc.DeleteChannelFromUserFeedsParams) error
	DeleteFeedFn                 func(context.Context, sqlc.DeleteFeedParams) (int64, error)
	DeleteFeedChannelsFn         func(context.Context, uuid.UUID) error
	DismissVideoFn               func(context.Context, sqlc.DismissVideoParams) error
	UndismissVideoFn             func(context.Context, sqlc.UndismissVideoParams) error
	ListDismissedForVideosFn     func(context.Context, sqlc.ListDismissedForVideosParams) ([]string, error)
	ListBrandingFn               func(context.Context, []string) ([]sqlc.DearrowBranding, error)
	UpsertBrandingFn             func(context.Context, sqlc.UpsertBrandingParams) error
	WatchTotalsFn                func(context.Context, sqlc.WatchTotalsParams) (sqlc.WatchTotalsRow, error)
	WatchTopChannelsFn           func(context.Context, sqlc.WatchTopChannelsParams) ([]sqlc.WatchTopChannelsRow, error)
	WatchByHourFn                func(context.Context, sqlc.WatchByHourParams) ([]sqlc.WatchByHourRow, error)
	WatchByWeekdayFn             func(context.Context, sqlc.WatchByWeekdayParams) ([]sqlc.WatchByWeekdayRow, error)
	WatchByMonthFn               func(context.Context, sqlc.WatchByMonthParams) ([]sqlc.WatchByMonthRow, error)
	GetFeedFn                    func(context.Context, sqlc.GetFeedParams) (sqlc.Feed, error)
	GetPrefsFn                   func(context.Context, uuid.UUID) ([]byte, error)
	GetUserFn                    func(context.Context, uuid.UUID) (sqlc.User, error)
	GetWatchEventFn              func(context.Context, sqlc.GetWatchEventParams) (sqlc.WatchEvent, error)
	HideHistoryEntryFn           func(context.Context, sqlc.HideHistoryEntryParams) (int64, error)
	ListFeedChannelsFn           func(context.Context, uuid.UUID) ([]string, error)
	ListFeedChannelsForUserFn    func(context.Context, uuid.UUID) ([]sqlc.ListFeedChannelsForUserRow, error)
	ListFeedsFn                  func(context.Context, uuid.UUID) ([]sqlc.Feed, error)
	ListHistoryFn                func(context.Context, sqlc.ListHistoryParams) ([]sqlc.WatchEvent, error)
	ListInProgressFn             func(context.Context, sqlc.ListInProgressParams) ([]sqlc.WatchEvent, error)
	ListPinnedPlaylistsFn        func(context.Context, uuid.UUID) ([]sqlc.PlaylistSetting, error)
	ListPlaylistSettingsFn       func(context.Context, uuid.UUID) ([]sqlc.PlaylistSetting, error)
	ListWatchEventsForVideosFn   func(context.Context, sqlc.ListWatchEventsForVideosParams) ([]sqlc.WatchEvent, error)
	PruneEmptyPlaylistSettingsFn func(context.Context, uuid.UUID) error
	SetPlaylistMusicFn           func(context.Context, sqlc.SetPlaylistMusicParams) error
	SetPlaylistPinnedFn          func(context.Context, sqlc.SetPlaylistPinnedParams) error
	NextFeedChannelPositionFn    func(context.Context, uuid.UUID) (int32, error)
	NextFeedPositionFn           func(context.Context, uuid.UUID) (int32, error)
	ResetPositionFn              func(context.Context, sqlc.ResetPositionParams) error
	SetFeedPositionFn            func(context.Context, sqlc.SetFeedPositionParams) error
	SetWatchedFn                 func(context.Context, sqlc.SetWatchedParams) (sqlc.WatchEvent, error)
	UnpinFeedsFn                 func(context.Context, uuid.UUID) error
	UpdateFeedFn                 func(context.Context, sqlc.UpdateFeedParams) (sqlc.Feed, error)
	UpsertPrefsFn                func(context.Context, sqlc.UpsertPrefsParams) error
	UpsertProgressFn             func(context.Context, sqlc.UpsertProgressParams) (sqlc.WatchEvent, error)
	UpsertUserFn                 func(context.Context, sqlc.UpsertUserParams) (sqlc.User, error)
}

func (f *FakeQuerier) AddFeedChannel(ctx context.Context, arg sqlc.AddFeedChannelParams) error {
	return f.AddFeedChannelFn(ctx, arg)
}

func (f *FakeQuerier) CountHistory(ctx context.Context, arg sqlc.CountHistoryParams) (int64, error) {
	return f.CountHistoryFn(ctx, arg)
}

func (f *FakeQuerier) CreateFeed(ctx context.Context, arg sqlc.CreateFeedParams) (sqlc.Feed, error) {
	return f.CreateFeedFn(ctx, arg)
}

func (f *FakeQuerier) DeleteChannelFromUserFeeds(ctx context.Context, arg sqlc.DeleteChannelFromUserFeedsParams) error {
	return f.DeleteChannelFromUserFeedsFn(ctx, arg)
}

func (f *FakeQuerier) DeleteFeed(ctx context.Context, arg sqlc.DeleteFeedParams) (int64, error) {
	return f.DeleteFeedFn(ctx, arg)
}

func (f *FakeQuerier) DeleteFeedChannels(ctx context.Context, feedID uuid.UUID) error {
	return f.DeleteFeedChannelsFn(ctx, feedID)
}

func (f *FakeQuerier) GetFeed(ctx context.Context, arg sqlc.GetFeedParams) (sqlc.Feed, error) {
	return f.GetFeedFn(ctx, arg)
}

func (f *FakeQuerier) GetPrefs(ctx context.Context, userID uuid.UUID) ([]byte, error) {
	return f.GetPrefsFn(ctx, userID)
}

func (f *FakeQuerier) GetUser(ctx context.Context, id uuid.UUID) (sqlc.User, error) {
	return f.GetUserFn(ctx, id)
}

func (f *FakeQuerier) GetWatchEvent(ctx context.Context, arg sqlc.GetWatchEventParams) (sqlc.WatchEvent, error) {
	return f.GetWatchEventFn(ctx, arg)
}

func (f *FakeQuerier) HideHistoryEntry(ctx context.Context, arg sqlc.HideHistoryEntryParams) (int64, error) {
	return f.HideHistoryEntryFn(ctx, arg)
}

func (f *FakeQuerier) ListFeedChannels(ctx context.Context, feedID uuid.UUID) ([]string, error) {
	return f.ListFeedChannelsFn(ctx, feedID)
}

func (f *FakeQuerier) ListFeedChannelsForUser(ctx context.Context, userID uuid.UUID) ([]sqlc.ListFeedChannelsForUserRow, error) {
	return f.ListFeedChannelsForUserFn(ctx, userID)
}

func (f *FakeQuerier) ListFeeds(ctx context.Context, userID uuid.UUID) ([]sqlc.Feed, error) {
	return f.ListFeedsFn(ctx, userID)
}

func (f *FakeQuerier) ListBranding(ctx context.Context, ids []string) ([]sqlc.DearrowBranding, error) {
	return f.ListBrandingFn(ctx, ids)
}

func (f *FakeQuerier) UpsertBranding(ctx context.Context, arg sqlc.UpsertBrandingParams) error {
	return f.UpsertBrandingFn(ctx, arg)
}

func (f *FakeQuerier) WatchTotals(ctx context.Context, arg sqlc.WatchTotalsParams) (sqlc.WatchTotalsRow, error) {
	return f.WatchTotalsFn(ctx, arg)
}

func (f *FakeQuerier) WatchTopChannels(ctx context.Context, arg sqlc.WatchTopChannelsParams) ([]sqlc.WatchTopChannelsRow, error) {
	return f.WatchTopChannelsFn(ctx, arg)
}

func (f *FakeQuerier) WatchByHour(ctx context.Context, arg sqlc.WatchByHourParams) ([]sqlc.WatchByHourRow, error) {
	return f.WatchByHourFn(ctx, arg)
}

func (f *FakeQuerier) WatchByWeekday(ctx context.Context, arg sqlc.WatchByWeekdayParams) ([]sqlc.WatchByWeekdayRow, error) {
	return f.WatchByWeekdayFn(ctx, arg)
}

func (f *FakeQuerier) WatchByMonth(ctx context.Context, arg sqlc.WatchByMonthParams) ([]sqlc.WatchByMonthRow, error) {
	return f.WatchByMonthFn(ctx, arg)
}

func (f *FakeQuerier) ListHistory(ctx context.Context, arg sqlc.ListHistoryParams) ([]sqlc.WatchEvent, error) {
	return f.ListHistoryFn(ctx, arg)
}

func (f *FakeQuerier) ListInProgress(ctx context.Context, arg sqlc.ListInProgressParams) ([]sqlc.WatchEvent, error) {
	return f.ListInProgressFn(ctx, arg)
}

func (f *FakeQuerier) ListWatchEventsForVideos(ctx context.Context, arg sqlc.ListWatchEventsForVideosParams) ([]sqlc.WatchEvent, error) {
	return f.ListWatchEventsForVideosFn(ctx, arg)
}

// The three dismissal queries below break the "unset panics" rule on purpose:
// every video listing asks which videos are dismissed, so a test about
// something else would otherwise have to stub a query it does not care about.
// Unset means "nobody has dismissed anything", which is the state a test that
// never mentions dismissal is describing.

func (f *FakeQuerier) ListDismissedForVideos(ctx context.Context, arg sqlc.ListDismissedForVideosParams) ([]string, error) {
	if f.ListDismissedForVideosFn == nil {
		return nil, nil
	}
	return f.ListDismissedForVideosFn(ctx, arg)
}

func (f *FakeQuerier) DismissVideo(ctx context.Context, arg sqlc.DismissVideoParams) error {
	if f.DismissVideoFn == nil {
		return nil
	}
	return f.DismissVideoFn(ctx, arg)
}

func (f *FakeQuerier) UndismissVideo(ctx context.Context, arg sqlc.UndismissVideoParams) error {
	if f.UndismissVideoFn == nil {
		return nil
	}
	return f.UndismissVideoFn(ctx, arg)
}

func (f *FakeQuerier) NextFeedChannelPosition(ctx context.Context, feedID uuid.UUID) (int32, error) {
	return f.NextFeedChannelPositionFn(ctx, feedID)
}

func (f *FakeQuerier) NextFeedPosition(ctx context.Context, userID uuid.UUID) (int32, error) {
	return f.NextFeedPositionFn(ctx, userID)
}

func (f *FakeQuerier) ResetPosition(ctx context.Context, arg sqlc.ResetPositionParams) error {
	return f.ResetPositionFn(ctx, arg)
}

func (f *FakeQuerier) SetFeedPosition(ctx context.Context, arg sqlc.SetFeedPositionParams) error {
	return f.SetFeedPositionFn(ctx, arg)
}

func (f *FakeQuerier) SetWatched(ctx context.Context, arg sqlc.SetWatchedParams) (sqlc.WatchEvent, error) {
	return f.SetWatchedFn(ctx, arg)
}

func (f *FakeQuerier) UnpinFeeds(ctx context.Context, userID uuid.UUID) error {
	return f.UnpinFeedsFn(ctx, userID)
}

func (f *FakeQuerier) UpdateFeed(ctx context.Context, arg sqlc.UpdateFeedParams) (sqlc.Feed, error) {
	return f.UpdateFeedFn(ctx, arg)
}

func (f *FakeQuerier) UpsertPrefs(ctx context.Context, arg sqlc.UpsertPrefsParams) error {
	return f.UpsertPrefsFn(ctx, arg)
}

func (f *FakeQuerier) UpsertProgress(ctx context.Context, arg sqlc.UpsertProgressParams) (sqlc.WatchEvent, error) {
	return f.UpsertProgressFn(ctx, arg)
}

func (f *FakeQuerier) UpsertUser(ctx context.Context, arg sqlc.UpsertUserParams) (sqlc.User, error) {
	return f.UpsertUserFn(ctx, arg)
}

func (f *FakeQuerier) ListPinnedPlaylists(ctx context.Context, userID uuid.UUID) ([]sqlc.PlaylistSetting, error) {
	return f.ListPinnedPlaylistsFn(ctx, userID)
}

func (f *FakeQuerier) ListPlaylistSettings(ctx context.Context, userID uuid.UUID) ([]sqlc.PlaylistSetting, error) {
	return f.ListPlaylistSettingsFn(ctx, userID)
}

func (f *FakeQuerier) PruneEmptyPlaylistSettings(ctx context.Context, userID uuid.UUID) error {
	return f.PruneEmptyPlaylistSettingsFn(ctx, userID)
}

func (f *FakeQuerier) SetPlaylistMusic(ctx context.Context, arg sqlc.SetPlaylistMusicParams) error {
	return f.SetPlaylistMusicFn(ctx, arg)
}

func (f *FakeQuerier) SetPlaylistPinned(ctx context.Context, arg sqlc.SetPlaylistPinnedParams) error {
	return f.SetPlaylistPinnedFn(ctx, arg)
}
