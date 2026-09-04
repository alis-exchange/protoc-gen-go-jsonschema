package plugin

import (
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// targetSet holds, by full name, every message some file in the plugin request
// targets — directly, or by forcing it as a dependency or nested message.
type targetSet map[protoreflect.FullName]bool

// collectTargets runs the collection over every file the request asks to
// generate and unions the results. A file consults it for messages it defines
// that a sibling file forced (a `generate = false` dependency referenced from
// elsewhere): the sibling's _WithDefs call must resolve, and only the defining
// file can emit the function. Files the request does not generate (imports)
// are outside the plugin's reach and are not consulted.
func collectTargets(gen *protogen.Plugin) targetSet {
	targets := make(targetSet)
	for _, f := range gen.Files {
		if !f.Generate {
			continue
		}
		for _, m := range getMessagesWithForce(f.Messages, fileGeneratesAll(f), false, make(map[string]bool)) {
			targets[m.Desc.FullName()] = true
		}
	}
	return targets
}

// fileGeneratesAll reports the file-level default: whether every message in
// the file generates unless it opts out.
func fileGeneratesAll(file *protogen.File) bool {
	if opts := getFileJsonSchemaOptions(file); opts != nil {
		return opts.GetGenerate()
	}
	return false
}

// forcedLocalMessages walks the messages defined in msgs (declaration order,
// nested messages included, map entries skipped) and returns those that
// another file in the request targets but this file's own collection has not
// visited. See collectTargets.
func forcedLocalMessages(msgs []*protogen.Message, targets targetSet, visited map[string]bool) []*protogen.Message {
	var out []*protogen.Message
	for _, m := range msgs {
		if m.Desc.IsMapEntry() {
			continue
		}
		if name := m.Desc.FullName(); targets[name] && !visited[string(name)] {
			out = append(out, m)
		}
		out = append(out, forcedLocalMessages(m.Messages, targets, visited)...)
	}
	return out
}

// getMessagesWithForce recursively collects all messages that should generate
// JSON Schema code.
//
// This implements the message filtering and dependency resolution logic:
//   - Skips internal proto types (map entries)
//   - Respects message-level options that can override the default generation flag
//   - Automatically includes message dependencies (messageReferences: every
//     non-ignored message-typed field, free-form well-known types excepted)
//   - Recursively processes nested message definitions
//   - Includes Google types (google.*) when referenced
//
// When force=true, explicit generate=false options are ignored: dependencies
// and nested messages of a generating parent must generate too, otherwise the
// parent's _WithDefs calls would not resolve.
//
// Parameters:
//   - messages: The list of messages to process (top-level or nested)
//   - defaultGenerate: Whether to generate by default (from file or parent options)
//   - force: Whether to override explicit generate=false
//   - visited: Tracks already-processed messages to prevent duplicates and infinite loops
//
// Returns a flat list of all messages that should generate schemas, in dependency order.
func getMessagesWithForce(messages []*protogen.Message, defaultGenerate bool, force bool, visited map[string]bool) []*protogen.Message {
	var results []*protogen.Message

	for _, message := range messages {
		// Map entries are synthetic messages created by protoc for map fields.
		// Maps are handled directly in field processing, not as separate messages.
		if message.Desc.IsMapEntry() {
			continue
		}

		// --- Determine Generation Flag ---
		// Start with the inherited default, then check for message-specific
		// override. The override is presence-based: only an explicitly set
		// `generate` counts — a message-level options block that merely
		// declares other options (e.g. json_schema.oneof) does not change
		// whether the message generates. If force=true, an explicit
		// generate=false is ignored to prevent broken $refs.
		shouldGen := defaultGenerate
		if opts := getMessageJsonSchemaOptions(message); opts != nil && opts.Generate != nil {
			optValue := *opts.Generate
			if force && !optValue {
				// When forced (e.g., by parent generating), ignore explicit false.
				shouldGen = defaultGenerate
			} else {
				shouldGen = optValue
			}
		}

		if !shouldGen {
			continue
		}

		messageName := string(message.Desc.FullName())

		// Only process each message once to avoid duplicates and infinite recursion.
		if !visited[messageName] {
			visited[messageName] = true
			results = append(results, message)

			// Recursively collect dependencies: every message the schema
			// refers to must generate one of its own, otherwise the parent's
			// _WithDefs call would not resolve. Force 'true' because
			// dependencies are required regardless of their own options.
			// Ignored fields and free-form well-known types are not
			// references (see messageReferences) and force nothing.
			for _, dep := range messageReferences(message) {
				results = append(results, getMessagesWithForce([]*protogen.Message{dep}, true, true, visited)...)
			}
		}

		// --- Process Nested Messages (ONLY when parent generates) ---
		// Nested messages are part of the parent's type system: if the parent
		// generates, all nested types generate too, so a reference to one
		// resolves whether it is inlined or reached through $defs.
		// Explicit generate=false on nested messages is ignored
		// (force=true), matching the field dependency logic.
		if len(message.Messages) > 0 {
			results = append(results, getMessagesWithForce(message.Messages, true, true, visited)...)
		}
	}

	return results
}
