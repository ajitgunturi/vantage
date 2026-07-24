package collector

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
)

// isPoisonErr must classify only deterministic row-level SQLSTATE classes
// (22 data exception, 23 integrity violation) as poison — transient classes
// must propagate for redelivery, never dead-letter.
func TestIsPoisonErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"check violation (23514)", &pgconn.PgError{Code: "23514"}, true},
		{"not-null violation (23502)", &pgconn.PgError{Code: "23502"}, true},
		{"numeric overflow (22003)", &pgconn.PgError{Code: "22003"}, true},
		{"invalid text rep (22P02)", &pgconn.PgError{Code: "22P02"}, true},
		{"wrapped poison", fmt.Errorf("persist: %w", &pgconn.PgError{Code: "23505"}), true},
		{"serialization failure (40001)", &pgconn.PgError{Code: "40001"}, false},
		{"connection failure (08006)", &pgconn.PgError{Code: "08006"}, false},
		{"admin shutdown (57P01)", &pgconn.PgError{Code: "57P01"}, false},
		{"non-pg error", errors.New("dial tcp: connection refused"), false},
		{"ctx cancelled", context.Canceled, false},
		{"empty code", &pgconn.PgError{Code: ""}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isPoisonErr(tc.err))
		})
	}
}
