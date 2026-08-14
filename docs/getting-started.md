---
title: Getting started
nav_order: 2
---

# Getting started

## 1. Install the plugin

All dependencies resolve from the public Go module proxy — no special
configuration:

```shell
go install github.com/alis-exchange/protoc-gen-go-jsonschema/cmd/protoc-gen-go-jsonschema@latest
```

Or download a pre-built binary for your platform from the
[releases page](https://github.com/alis-exchange/protoc-gen-go-jsonschema/releases)
and put it on your `PATH` as `protoc-gen-go-jsonschema`.

## 2. Get the options proto

Schema generation is opt-in, controlled by custom options from
`alis/open/options/v1/options.proto`. Your protos import it, and `protoc`
needs it on the include path. It ships with the
[alis-build/common-protos](https://github.com/alis-build/common-protos)
repository; its Go bindings are the public module
`go.alis.build/common/alis/open/options`.

If you use `buf`, add the dependency to your `buf.yaml`; with plain `protoc`,
pass an extra `--proto_path` containing `alis/open/options/v1/options.proto`.

## 3. Annotate a proto

```protobuf
syntax = "proto3";

package weather.v1;

import "alis/open/options/v1/options.proto";

option go_package = "example.com/weather/v1;weatherv1";

// Generate schemas for every message in this file.
option (alis.open.options.v1.file).json_schema.generate = true;

message GetForecastRequest {
  // City name to look up.
  string city = 1;
  // Days ahead to forecast.
  int32 days_ahead = 2 [(alis.open.options.v1.field).json_schema = {
    minimum: 1,
    maximum: 14
  }];
}
```

Generation can be enabled for a whole file (as above) or per message with
`option (alis.open.options.v1.message).json_schema.generate = true;`.
Messages referenced by a generating message are always included, so `$ref`
pointers resolve. See the [options reference](options.md).

## 4. Run the plugin

With `protoc`:

```shell
protoc \
  --plugin=protoc-gen-go-jsonschema=$(which protoc-gen-go-jsonschema) \
  --go-jsonschema_out=. \
  --go-jsonschema_opt=paths=source_relative \
  --proto_path=. --proto_path=path/to/common-protos \
  weather/v1/weather.proto
```

With `buf` (`buf.gen.yaml`):

```yaml
version: v2
plugins:
  - local: protoc-gen-go-jsonschema
    out: gen/go
    opt: paths=source_relative
```

Run the plugin alongside `protoc-gen-go` — the generated
`*_jsonschema.pb.go` file lives in the same Go package as the regular
`*.pb.go` types and attaches methods to them.

**Important:** all proto files that share one Go package must be compiled in
the same invocation, so cross-file schema references resolve at compile time.

## 5. Use the schema

```go
import weatherv1 "example.com/weather/v1"

schema := (&weatherv1.GetForecastRequest{}).JsonSchema()

data, _ := json.Marshal(schema)   // serialize the schema itself
fmt.Println(string(data))

resolved, err := schema.Resolve(nil) // validate documents against it
if err != nil { ... }
err = resolved.Validate(map[string]any{"city": "Nairobi", "days_ahead": 3})
```

The only runtime dependency of the generated code is
`github.com/google/jsonschema-go` (plus `encoding/json` when defaults are
used). See [Generated code](generated-code.md) for what the plugin emits and
[MCP integration](mcp.md) for handing schemas to MCP tools.
