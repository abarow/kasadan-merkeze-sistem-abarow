module abarow.com/tools/natsauth

go 1.26.0

toolchain go1.27.4

require (
	abarow.com/pkg v0.0.0-00010101000000-000000000000
	github.com/nats-io/nats-server/v2 v2.12.15
	github.com/nats-io/nats.go v1.55.0
	github.com/nats-io/nkeys v0.4.16
)

require (
	github.com/antithesishq/antithesis-sdk-go v0.7.2-default-no-op // indirect
	github.com/google/go-tpm v0.9.8 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/klauspost/compress v1.20.3 // indirect
	github.com/minio/highwayhash v1.0.4 // indirect
	github.com/nats-io/jwt/v2 v2.8.2 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	golang.org/x/crypto v0.58.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/time v0.15.0 // indirect
)

replace abarow.com/pkg => ../../pkg

replace abarow.com/gen => ../../gen
