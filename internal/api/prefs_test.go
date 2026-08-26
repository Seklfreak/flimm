package api

import "testing"

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
