package api

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Seklfreak/flimm/internal/db/sqlc"
	"github.com/Seklfreak/flimm/internal/ta"
)

func TestPrefsDefaultSubtitleLangIsEnglish(t *testing.T) {
	if got := defaultPrefs().SubtitleLang; got != "en" {
		t.Errorf("default subtitle_lang = %q, want en", got)
	}
}

func TestParsePrefsSubtitleLang(t *testing.T) {
	cases := map[string]struct{ raw, want string }{
		"no row":             {"", "en"},
		"legacy null":        {`{"subtitle_lang":null}`, "en"},
		"legacy empty":       {`{"subtitle_lang":""}`, "en"},
		"explicit off kept":  {`{"subtitle_lang":"off"}`, "off"},
		"explicit lang":      {`{"subtitle_lang":"de"}`, "de"},
		"regional kept":      {`{"subtitle_lang":"en-US"}`, "en-US"},
		"garbage falls back": {`{"subtitle_lang":"!!"}`, "en"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := parsePrefs([]byte(c.raw)).SubtitleLang; got != c.want {
				t.Errorf("subtitle_lang = %q, want %q", got, c.want)
			}
		})
	}
}

// Every category has an answer, whatever the row was written before: a
// category added to Flimm later must come back at its default rather than
// missing, or a client cannot tell "leave it alone" from "not asked yet".
func TestSponsorActionsFillInCategoriesTheRowNeverHad(t *testing.T) {
	got := parsePrefs([]byte(`{"sponsor_actions":{"sponsor":"off"}}`))
	if got.SponsorActions["sponsor"] != "off" {
		t.Errorf("sponsor = %q, want the stored off", got.SponsorActions["sponsor"])
	}
	if got.SponsorActions["intro"] != "ask" {
		t.Errorf("intro = %q, want the default ask", got.SponsorActions["intro"])
	}
	if len(got.SponsorActions) != len(defaultSponsorActions()) {
		t.Errorf("actions = %v, want every category", got.SponsorActions)
	}
}

// The three that interrupt a video without being part of it are skipped; the
// rest are offered, because an intro or a recap is sometimes what a viewer
// came for.
func TestSponsorActionDefaults(t *testing.T) {
	d := defaultPrefs().SponsorActions
	for _, c := range []string{"sponsor", "selfpromo", "interaction"} {
		if d[c] != "skip" {
			t.Errorf("%s = %q, want skip", c, d[c])
		}
	}
	for _, c := range []string{"intro", "outro", "preview", "filler", "music_offtopic", "exclusive_access"} {
		if d[c] != "ask" {
			t.Errorf("%s = %q, want ask", c, d[c])
		}
	}
	if _, ok := d["poi_highlight"]; ok {
		t.Error("the highlight marks an instant; it is offered, never configured")
	}
}

// A category Flimm does not know, or an action it does not have, is refused
// rather than stored: a client reading it back would not know what to do.
func TestSponsorActionsRejectNonsense(t *testing.T) {
	var stored []byte
	q := newEventStore().querier()
	q.GetPrefsFn = func(context.Context, uuid.UUID) ([]byte, error) {
		if stored == nil {
			return nil, pgx.ErrNoRows
		}
		return stored, nil
	}
	q.UpsertPrefsFn = func(_ context.Context, arg sqlc.UpsertPrefsParams) error { stored = arg.Prefs; return nil }
	h := newTestServer(ta.NewFake(), q).Router()

	if rec := do(t, h, http.MethodPatch, "/api/v1/me/prefs",
		`{"sponsor_actions":{"sponsor":"maybe"}}`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad action: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, h, http.MethodPatch, "/api/v1/me/prefs",
		`{"sponsor_actions":{"poi_highlight":"skip"}}`); rec.Code != http.StatusBadRequest {
		t.Errorf("the highlight is not configurable: %d", rec.Code)
	}
	rec := do(t, h, http.MethodPatch, "/api/v1/me/prefs", `{"sponsor_actions":{"intro":"skip","sponsor":"off"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	got := decode[Prefs](t, rec)
	if got.SponsorActions["intro"] != "skip" || got.SponsorActions["sponsor"] != "off" {
		t.Errorf("actions = %v", got.SponsorActions)
	}
	// Categories the patch left out keep their defaults rather than vanishing.
	if got.SponsorActions["outro"] != "ask" {
		t.Errorf("outro = %q, want the default ask", got.SponsorActions["outro"])
	}
}

// Every preference has to be patchable. `prefKeys` is a hand-written
// allowlist, so a field added to Prefs without a line here comes back as
// "unknown pref" from the only endpoint that sets it — which is exactly how
// `normalize_loudness` shipped broken for an afternoon.
func TestEveryPrefIsPatchable(t *testing.T) {
	fields := reflect.VisibleFields(reflect.TypeOf(Prefs{}))
	for _, f := range fields {
		key := strings.Split(f.Tag.Get("json"), ",")[0]
		if key == "" || key == "-" {
			continue
		}
		if !prefKeys[key] {
			t.Errorf("Prefs.%s is sent as %q, which PATCH /me/prefs rejects: add it to prefKeys", f.Name, key)
		}
	}
	for key := range prefKeys {
		found := false
		for _, f := range fields {
			if strings.Split(f.Tag.Get("json"), ",")[0] == key {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("prefKeys has %q, which is not a field of Prefs", key)
		}
	}
}
