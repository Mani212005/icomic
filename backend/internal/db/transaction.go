package db

import (
	"context"
	"database/sql"
)

// Row and Transaction are the small SQL seam used by generation and credit writes.
// Tests can stage writes until Commit and discard them on Rollback without a server.
type Row interface{ Scan(...any) error }
type Transaction interface {
	QueryRowContext(context.Context, string, ...any) Row
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	Commit() error
	Rollback() error
}
type TransactionStarter interface {
	Begin(context.Context) (Transaction, error)
}

type sqlTransactions struct{ pool *sql.DB }
type sqlTransaction struct{ *sql.Tx }

func (t sqlTransaction) QueryRowContext(ctx context.Context, query string, args ...any) Row {
	return t.Tx.QueryRowContext(ctx, query, args...)
}
func (s sqlTransactions) Begin(ctx context.Context) (Transaction, error) {
	tx, err := s.pool.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return sqlTransaction{tx}, nil
}

// NewPostgresRepositoryWithDB reuses a caller-owned SQL pool. The caller closes it.
// This is also the entry point for isolated database integration tests.
func NewPostgresRepositoryWithDB(pool *sql.DB) *PostgresRepository {
	return NewPostgresRepositoryWithTransactions(pool, sqlTransactions{pool})
}

// NewPostgresRepositoryWithTransactions allows hermetic transaction failure injection.
func NewPostgresRepositoryWithTransactions(pool *sql.DB, starter TransactionStarter) *PostgresRepository {
	return &PostgresRepository{db: pool, transactions: starter}
}
