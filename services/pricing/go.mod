module github.com/izgo/pricing

go 1.26.0

require (
	github.com/jackc/pgx/v5 v5.13.0
	github.com/kelseyhightower/envconfig v1.4.0
	github.com/redis/go-redis/v9 v9.23.0
)

require (
	github.com/alicebob/miniredis/v2 v2.40.0
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/fergusstrange/embedded-postgres v1.34.0
	github.com/izgo/shared v0.0.0
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.3 // indirect
	github.com/lib/pq v1.10.9 // indirect
	github.com/xi2/xz v0.0.0-20171230120015-48954b6210f8 // indirect
	github.com/yuin/gopher-lua v1.1.1 // indirect
	go.uber.org/atomic v1.12.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.41.0 // indirect
)

replace github.com/izgo/shared => ../../shared
