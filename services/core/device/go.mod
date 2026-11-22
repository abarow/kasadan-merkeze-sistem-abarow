module abarow.com/services/core/device

go 1.26.0

toolchain go1.27.4

require (
	abarow.com/gen v0.0.0-00010101000000-000000000000
	abarow.com/pkg v0.0.0-00010101000000-000000000000
	golang.org/x/sys v0.50.0
	golang.org/x/text v0.44.0
	google.golang.org/grpc v1.85.0
)

require (
	golang.org/x/net v0.62.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20261110000943-09e21b237f2c // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)

replace (
	abarow.com/gen => ../../../gen
	abarow.com/pkg => ../../../pkg
)
