package plugin

import "google.golang.org/protobuf/compiler/protogen"

// Generate generates JSON Schema code for all files in the plugin request.
// The version parameter is included in generated file headers for traceability.
func Generate(plugin *protogen.Plugin, version string) error {
	// Collection runs over the whole request first, so a file can generate
	// messages it defines that another file forced (see collectTargets).
	targets := collectTargets(plugin)

	for _, f := range plugin.Files {
		if !f.Generate {
			continue
		}

		generator := Generator{Version: version}

		if _, err := generator.generateFile(plugin, f, targets); err != nil {
			plugin.Error(err)
			return err
		}
	}

	return nil
}
