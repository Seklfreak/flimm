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
	SetPlaylistAudioOnlyFn       func(context.Context, sqlc.SetPlaylistAudioOnlyParams) error
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

func (f *FakeQuerier) ListHistory(ctx context.Context, arg sqlc.ListHistoryParams) ([]sqlc.WatchEvent, error) {
	return f.ListHistoryFn(ctx, arg)
}

func (f *FakeQuerier) ListInProgress(ctx context.Context, arg sqlc.ListInProgressParams) ([]sqlc.WatchEvent, error) {
	return f.ListInProgressFn(ctx, arg)
}

func (f *FakeQuerier) ListWatchEventsForVideos(ctx context.Context, arg sqlc.ListWatchEventsForVideosParams) ([]sqlc.WatchEvent, error) {
	return f.ListWatchEventsForVideosFn(ctx, arg)
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

func (f *FakeQuerier) SetPlaylistAudioOnly(ctx context.Context, arg sqlc.SetPlaylistAudioOnlyParams) error {
	return f.SetPlaylistAudioOnlyFn(ctx, arg)
}

func (f *FakeQuerier) SetPlaylistPinned(ctx context.Context, arg sqlc.SetPlaylistPinnedParams) error {
	return f.SetPlaylistPinnedFn(ctx, arg)
}
