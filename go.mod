module github.com/NguyenHuuHoang711/asao-odoo-tools

go 1.26.6

require (
	github.com/asao-platform/atoc/packages/proto v0.0.0-00010101000000-000000000000
	github.com/lib/pq v1.10.9
	github.com/redis/go-redis/v9 v9.22.0
	google.golang.org/grpc v1.82.1
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	golang.org/x/text v0.39.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260414002931-afd174a4e478 // indirect
)

replace github.com/asao-platform/atoc/packages/proto => ./proto
