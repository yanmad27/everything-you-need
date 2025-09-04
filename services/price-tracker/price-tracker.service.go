package pricetracker

import (
	"fmt"
	"net/http"
	"time"
)


type PriceService struct {
	sources []PriceSource
	client  *http.Client
}

func NewPriceService() *PriceService {
	return &PriceService{
		sources: make([]PriceSource, 0),
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

func (g *PriceService) AddSource(source PriceSource) {
	g.sources = append(g.sources, source)
}

func (g *PriceService) GetAllPrices() (map[string][]Price, error) {
	results := make(map[string][]Price)
	
	for _, source := range g.sources {
		prices, err := source.GetPrices()
		if err != nil {
			results[source.GetSourceName()] = []Price{}
			continue
		}
		results[source.GetSourceName()] = prices
	}
	
	return results, nil
}

func (g *PriceService) GetPricesFromSource(sourceName string) ([]Price, error) {
	for _, source := range g.sources {
		if source.GetSourceName() == sourceName {
			return source.GetPrices()
		}
	}
	return nil, fmt.Errorf("source %s not found", sourceName)
}