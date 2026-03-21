package store

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
)

type txQueryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *Store) bind(query string) string {
	if s == nil {
		return query
	}

	var builder strings.Builder
	builder.Grow(len(query) + 8)

	index := 1
	for _, r := range query {
		if r == '?' {
			builder.WriteByte('$')
			builder.WriteString(strconv.Itoa(index))
			index++
			continue
		}
		builder.WriteRune(r)
	}

	return builder.String()
}

func (s *Store) insertID(ctx context.Context, runner txQueryRower, query string, args ...any) (int64, error) {
	var id int64
	if err := runner.QueryRowContext(ctx, s.bind(query), args...).Scan(&id); err != nil {
		return 0, err
	}

	return id, nil
}
