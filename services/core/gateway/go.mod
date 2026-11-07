module abarow.com/services/core/gateway

go 1.26.0

require (
	abarow.com/gen v0.0.0-00010101000000-000000000000
	abarow.com/pkg v0.0.0-00010101000000-000000000000
	google.golang.org/grpc v1.84.0
	google.golang.org/protobuf v1.36.12
)

require (
	golang.org/x/net v0.62.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.43.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20261005182115-fad411399dd8 // indirect
)

replace (
	abarow.com/gen => ../../../gen
	abarow.com/pkg => ../../../pkg
)
