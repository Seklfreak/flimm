package config

import (
	"testing"

	"github.com/Seklfreak/flimm/internal/sponsorblock"
)

// Setting a variable to nothing has to mean something different from not
// setting it: `SPONSORBLOCK_URL=` is how a deployment with no egress turns the
// lookup off, and falling back to the default there would send it to the
// public service anyway.
func TestSponsorblockURLEmptyMeansDisabled(t *testing.T) {
	setRequired(t)

	t.Setenv("SPONSORBLOCK_URL", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.SponsorblockURL != "" {
		t.Errorf("SponsorblockURL = %q, want empty (the lookup disabled)", cfg.SponsorblockURL)
	}
}

func TestSponsorblockURLDefaultsWhenUnset(t *testing.T) {
	setRequired(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.SponsorblockURL != sponsorblock.DefaultBaseURL {
		t.Errorf("SponsorblockURL = %q, want %q", cfg.SponsorblockURL, sponsorblock.DefaultBaseURL)
	}
}

func TestSponsorblockURLTrimsATrailingSlash(t *testing.T) {
	setRequired(t)

	t.Setenv("SPONSORBLOCK_URL", "https://sb.example.com/")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.SponsorblockURL != "https://sb.example.com" {
		t.Errorf("SponsorblockURL = %q", cfg.SponsorblockURL)
	}
}

// setRequired sets the variables Load insists on, so each test only has to
// say what it is actually about.
func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("TA_URL", "http://tubearchivist.example.com")
	t.Setenv("TA_TOKEN", "token")
	t.Setenv("DATABASE_URL", "postgres://localhost/flimm")
	t.Setenv("MEDIA_TOKEN_SECRET", "secret")
	t.Setenv("PUBLIC_URL", "https://flimm.example.com")
	t.Setenv("AUTH_DISABLED", "true")
}
