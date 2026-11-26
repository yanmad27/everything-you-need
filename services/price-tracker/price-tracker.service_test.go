package pricetracker

import (
	"testing"
	"time"

	"everything-you-need/m/services/config"
)

func TestKimKhoaSourceRegisteredOnlyWhenEnabled(t *testing.T) {
	has := func(s *PriceTrackerService) bool {
		for _, src := range s.sources {
			if src.GetSourceName() == KimKhoaSourceName {
				return true
			}
		}
		return false
	}

	if has(NewPriceTrackerService(&config.Config{})) {
		t.Fatal("kimkhoa source registered while disabled")
	}

	cfg := &config.Config{}
	cfg.PriceSources.KimKhoa.Enabled = true
	if !has(NewPriceTrackerService(cfg)) {
		t.Fatal("kimkhoa source missing when enabled")
	}
}

func TestKimKhoaListedByReaderWhenEnabled(t *testing.T) {
	disabled := NewPriceTrackerService(&config.Config{})
	for _, name := range NewCachedPriceReader(disabled, time.Minute).Sources() {
		if name == KimKhoaSourceName {
			t.Fatalf("reader lists %q while disabled", name)
		}
	}
	if _, err := disabled.GetPricesFromSource(KimKhoaSourceName); err == nil {
		t.Fatal("lookup of a disabled source must return an error, not prices")
	}

	cfg := &config.Config{}
	cfg.PriceSources.KimKhoa.Enabled = true
	found := false
	for _, name := range NewCachedPriceReader(NewPriceTrackerService(cfg), time.Minute).Sources() {
		found = found || name == KimKhoaSourceName
	}
	if !found {
		t.Fatal("reader must list the kimkhoa source when enabled")
	}
}
