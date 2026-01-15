package main

import (
	"flag"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/alis-exchange/protoc-gen-go-jsonschema/plugin"
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/types/pluginpb"
)

// version can be set at build time via ldflags
var version string

func getVersion() string {
	// If version was set via ldflags, use it
	if version != "" {
		return version
	}

	// Try to get version from Go module info (works with go install)
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}

	return "development"
}

func main() {
	var flags flag.FlagSet

	// Get the flags
	showVersion := flag.Bool("version", false, "Print the version of protoc-gen-go-jsonschema")
	flag.Parse()

	if *showVersion {
		fmt.Printf("%s\n", getVersion())
		os.Exit(0)
	}

	options := protogen.Options{
		ParamFunc: flags.Set,
	}

	version := getVersion()

	options.Run(func(p *protogen.Plugin) error {
		p.SupportedFeatures = uint64(pluginpb.CodeGeneratorResponse_FEATURE_PROTO3_OPTIONAL)
		return plugin.Generate(p, version)
	})
}
