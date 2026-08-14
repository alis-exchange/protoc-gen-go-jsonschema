package plugin

import (
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// getMessagesWithForce recursively collects all messages that should generate
// JSON Schema code.
//
// This implements the message filtering and dependency resolution logic:
//   - Skips internal proto types (map entries)
//   - Respects message-level options that can override the default generation flag
//   - Automatically includes message dependencies (fields that reference other messages)
//   - Recursively processes nested message definitions
//   - Includes Google types (google.*) when referenced
//
// When force=true, explicit generate=false options are ignored: dependencies
// and nested messages of a generating parent must generate too, otherwise the
// parent's $ref pointers would be broken.
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

			// Recursively collect dependencies: any message-type field must also
			// generate a schema, otherwise the $ref in the parent would be broken.
			// Force 'true' because dependencies are required regardless of their
			// own options.
			for _, field := range message.Fields {
				if field.Desc.Kind() != protoreflect.MessageKind {
					continue
				}
				if field.Desc.IsMap() {
					// For map fields, collect the value type, not the synthetic
					// map entry. The value message lives on field 2.
					if field.Desc.MapValue().Kind() == protoreflect.MessageKind {
						for _, f := range field.Message.Fields {
							if f.Desc.Number() == 2 && f.Message != nil {
								results = append(results, getMessagesWithForce([]*protogen.Message{f.Message}, true, true, visited)...)
								break
							}
						}
					}
				} else {
					results = append(results, getMessagesWithForce([]*protogen.Message{field.Message}, true, true, visited)...)
				}
			}
		}

		// --- Process Nested Messages (ONLY when parent generates) ---
		// Nested messages are part of the parent's type system: if the parent
		// generates, all referenced nested types MUST generate so that $ref
		// pointers like "#/$defs/Parent.Child" resolve. Explicit generate=false
		// on nested messages is ignored (force=true), matching the field
		// dependency logic.
		if len(message.Messages) > 0 {
			results = append(results, getMessagesWithForce(message.Messages, true, true, visited)...)
		}
	}

	return results
}
