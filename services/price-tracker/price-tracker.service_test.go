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

func TestDisabledKimKhoaAbsentFromReaderAndLookup(t *testing.T) {
	svc := NewPriceTrackerService(&config.Config{})
	for _, name := range NewCachedPriceReader(svc, time.Minute).Sources() {
		if name == KimKhoaSourceName {
			t.Fatalf("reader still lists %q", name)
		}
	}
	if _, err := svc.GetPricesFromSource(KimKhoaSourceName); err == nil {
		t.Fatal("lookup of a disabled source must return an error, not prices")
	}
}
