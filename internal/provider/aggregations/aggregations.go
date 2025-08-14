package aggregations

import (
	"github.com/PeterChen1997/synctv/internal/provider"
)

var allAggregation []provider.AggregationProviderInterface

func addAggregation(ps ...provider.AggregationProviderInterface) {
	allAggregation = append(allAggregation, ps...)
}

func AllAggregation() []provider.AggregationProviderInterface {
	return allAggregation
}
