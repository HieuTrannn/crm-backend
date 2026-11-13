package database

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// ViolatesConstraint reports whether err is a PostgreSQL error raised by the
// named constraint or unique index (unique, foreign key or check violation).
func ViolatesConstraint(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.ConstraintName == constraint
}
