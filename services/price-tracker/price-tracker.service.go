package pricetracker

import (
	"everything-you-need/m/services/config"
	"fmt"
	"net/http"
	"time"

	"github.com/submodule-org/submodule.go/v2"
)

type PriceTrackerService struct {
	sources []PriceSource
	client  *http.Client
}

func NewPriceTrackerService(config *config.Config) *PriceTrackerService {
	service := &PriceTrackerService{
		sources: make([]PriceSource, 0),
		client:  &http.Client{Timeout: 30 * time.Second},
	}

	if config.PriceSources.Doji.Enabled {
		dojiSource := NewDojiSource(config.PriceSources.Doji.APIURL)
		service.AddSource(dojiSource)
		fmt.Println("✓ Added Doji source for gold prices")
	}

	if config.PriceSources.BTMC.Enabled {
		btmcSource := NewBTMCSource(config.PriceSources.BTMC.APIURL)
		service.AddSource(btmcSource)
		fmt.Println("✓ Added BTMC source for gold prices")
	}

	return service
}

var PriceTrackerServiceMod = submodule.Make[*PriceTrackerService](NewPriceTrackerService, config.ConfigMod)

func (g *PriceTrackerService) AddSource(source PriceSource) {
	g.sources = append(g.sources, source)
}

func (g *PriceTrackerService) GetAllPrices() (map[string][]Price, error) {
	results := make(map[string][]Price)
	errs := make([]error, 0)

	for _, source := range g.sources {
		prices, err := source.GetPrices()
		if err != nil {
			results[source.GetSourceName()] = []Price{}
			errs = append(errs, err)
		} else {
			results[source.GetSourceName()] = prices
		}
	}

	if len(errs) > 0 {
		return results, fmt.Errorf("failed to fetch prices from some sources")
	}

	return results, nil
}

func (g *PriceTrackerService) GetPricesFromSource(sourceName string) ([]Price, error) {
	for _, source := range g.sources {
		if source.GetSourceName() == sourceName {
			return source.GetPrices()
		}
	}
	return nil, fmt.Errorf("source %s not found", sourceName)
}
