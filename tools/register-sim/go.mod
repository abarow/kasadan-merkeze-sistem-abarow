module abarow.com/tools/register-sim

go 1.26.0

require (
	abarow.com/gen v0.0.0-00010101000000-000000000000
	abarow.com/pkg v0.0.0-00010101000000-000000000000
	github.com/nats-io/nats.go v1.55.0
	google.golang.org/grpc v1.84.0
	google.golang.org/protobuf v1.36.12
)

require (
	github.com/klauspost/compress v1.20.3 // indirect
	github.com/nats-io/nkeys v0.4.16 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	golang.org/x/crypto v0.58.0 // indirect
	golang.org/x/net v0.62.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.43.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20261005182115-fad411399dd8 // indirect
)

replace (
	abarow.com/gen => ../../gen
	abarow.com/pkg => ../../pkg
)
