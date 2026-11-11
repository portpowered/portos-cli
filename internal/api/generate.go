//go:build interfaces
// +build interfaces

//go:generate bun ../../../scripts/generateGoOpenAPI.mjs ../../../packages/reference-data/generated/public/docs/references/openapi.yaml codegen_config/config.yaml models.gen.go
//go:generate go run generators/message_types.go ../../../packages/reference-data/generated/public/docs/references/openapi.yaml
package api
