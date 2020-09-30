package repository

import (
	"context"
)

type Repository interface {
	Health(ctx context.Context) error

	// PushEvents adds all events of the given aggregates to the eventstreams of the aggregates.
	// This call is transaction save. The transaction will be rolled back if one event fails
	Push(ctx context.Context, events ...*Event) error
	// Filter returns all events matching the given search query
	Filter(ctx context.Context, searchQuery *SearchQuery) (events []*Event, err error)
	//LatestSequence returns the latests sequence found by the the search query
	LatestSequence(ctx context.Context, queryFactory *SearchQuery) (uint64, error)
}