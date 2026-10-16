package testutil

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenPostgresRejectsNonDisposableTargetsBeforeConnecting(t *testing.T) {
	for _, dsn := range []string{
		"postgres://audit@production.invalid/cilai_pr3_test?sslmode=disable",
		"postgres://audit@127.0.0.1/production?sslmode=disable",
		"postgres://audit@127.0.0.1/cilai_pr3_test", // prefer-TLS adds a fallback connection
	} {
		t.Run(dsn, func(t *testing.T) {
			db, err := OpenPostgres(dsn)
			require.ErrorContains(t, err, "loopback cilai_pr3_*")
			require.Nil(t, db)
		})
	}
}
