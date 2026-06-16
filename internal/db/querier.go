package db

import (
	"context"

	"github.com/waystones/oapif-go/internal/config"
)

// Querier is the subset of Store that Handler needs for query execution.
// Defining it as an interface allows tests to inject a mock without DuckDB.
type Querier interface {
	QueryItems(ctx context.Context, col *config.CollectionConfig, bucket string, opts QueryOptions) ([]Feature, int64, error)
	QueryItem(ctx context.Context, col *config.CollectionConfig, bucket, featureID, outputCRS string, properties []string) (*Feature, error)
	QueryAdjacentIDs(ctx context.Context, col *config.CollectionConfig, bucket, featureID string) (prevID, nextID string, err error)
}

// Compile-time assertion: *Store must satisfy Querier.
var _ Querier = (*Store)(nil)
