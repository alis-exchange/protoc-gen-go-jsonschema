package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/alis-exchange/protoc-gen-go-jsonschema/plugin"
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/types/pluginpb"
)

var version string // This will be set at build time

func main() {
	var flags flag.FlagSet

	// Get the flags
	showVersion := flag.Bool("version", false, "Print the version of protoc-gen-go-jsonschema")
	flag.Parse()

	if *showVersion {
		if version == "" {
			version = "development" // Default version if not provided at build time
		}
		fmt.Printf("%s\n", version)
		os.Exit(0)
	}

	options := protogen.Options{
		ParamFunc: flags.Set,
	}

	options.Run(func(p *protogen.Plugin) error {
		p.SupportedFeatures = uint64(pluginpb.CodeGeneratorResponse_FEATURE_PROTO3_OPTIONAL)
		return plugin.Generate(p)
	})
}
