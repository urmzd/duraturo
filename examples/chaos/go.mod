module github.com/urmzd/duraturo/examples/chaos

go 1.26.4

require (
	github.com/jackc/pgx/v5 v5.7.5
	github.com/redis/go-redis/v9 v9.21.0
	github.com/urmzd/duraturo v0.1.0
	github.com/urmzd/duraturo/adapters/postgres v0.0.0
	github.com/urmzd/duraturo/adapters/redis v0.0.0
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/crypto v0.37.0 // indirect
	golang.org/x/sync v0.13.0 // indirect
	golang.org/x/text v0.24.0 // indirect
)

replace (
	github.com/urmzd/duraturo => ../..
	github.com/urmzd/duraturo/adapters/postgres => ../../adapters/postgres
	github.com/urmzd/duraturo/adapters/redis => ../../adapters/redis
)
