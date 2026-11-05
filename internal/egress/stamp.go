package egress

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// StampTraces rewrites an OTLP/JSON traces request so every resource names
// the loop it came from (ADR-0064): spool.loop.id and spool.loop.name are
// set on each resource, overwriting whatever the loop sent, and any
// spool.loop.* attribute is removed from scopes, spans, and spans' events
// and links, which Langfuse may show beside the resource's. A trace
// naming another loop is rewritten, not refused, so a misbehaving loop
// shows up under its own name.
//
// Fields it doesn't know pass through as they came. A field it would have
// to walk but finds under another spelling is refused instead: protobuf's
// JSON mapping takes snake_case names too (resource_spans), and a body the
// relay walked past would reach Langfuse unstamped.
func StampTraces(body []byte, loop RelayLoop) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var request map[string]any
	if err := decoder.Decode(&request); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing data after the request")
	}
	if err := refuseSpellings(request, "resource_spans"); err != nil {
		return nil, err
	}
	resourceSpans, err := objects(request, "resourceSpans")
	if err != nil {
		return nil, err
	}
	for _, resourceSpan := range resourceSpans {
		if err := stampResource(resourceSpan, loop); err != nil {
			return nil, err
		}
	}
	return json.Marshal(request)
}

func stampResource(resourceSpan map[string]any, loop RelayLoop) error {
	if err := refuseSpellings(resourceSpan, "scope_spans", "instrumentationLibrarySpans", "instrumentation_library_spans"); err != nil {
		return err
	}
	resource, err := object(resourceSpan, "resource")
	if err != nil {
		return err
	}
	attributes, err := withoutIdentity(resource)
	if err != nil {
		return err
	}
	resource["attributes"] = append(attributes,
		stringAttribute(relayIdentityPrefix+"id", loop.ID),
		stringAttribute(relayIdentityPrefix+"name", loop.Name))
	resourceSpan["resource"] = resource

	scopeSpans, err := objects(resourceSpan, "scopeSpans")
	if err != nil {
		return err
	}
	for _, scopeSpan := range scopeSpans {
		if err := refuseSpellings(scopeSpan, "instrumentation_library"); err != nil {
			return err
		}
		if _, present := scopeSpan["scope"]; present {
			scope, err := object(scopeSpan, "scope")
			if err != nil {
				return err
			}
			if err := stripIdentity(scope); err != nil {
				return err
			}
		}
		spans, err := objects(scopeSpan, "spans")
		if err != nil {
			return err
		}
		for _, span := range spans {
			if err := stripSpan(span); err != nil {
				return err
			}
		}
	}
	return nil
}

// stripSpan removes every spool.loop.* attribute from span and from its
// events and links, so no part of a span carries a stamp the relay
// didn't write.
func stripSpan(span map[string]any) error {
	if err := stripIdentity(span); err != nil {
		return err
	}
	for _, name := range []string{"events", "links"} {
		entries, err := objects(span, name)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := stripIdentity(entry); err != nil {
				return err
			}
		}
	}
	return nil
}

// stripIdentity removes every spool.loop.* attribute from holder.
func stripIdentity(holder map[string]any) error {
	attributes, err := withoutIdentity(holder)
	if err != nil {
		return err
	}
	if _, present := holder["attributes"]; present {
		holder["attributes"] = attributes
	}
	return nil
}

// withoutIdentity is holder's attributes with every spool.loop.* one left
// out. The key is compared as written: OTLP keys are case-sensitive, so
// Spool.Loop.Id is another attribute, and not the stamp.
func withoutIdentity(holder map[string]any) ([]any, error) {
	raw, present := holder["attributes"]
	if !present || raw == nil {
		return []any{}, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, errors.New("attributes is not a list")
	}
	kept := make([]any, 0, len(list))
	for _, item := range list {
		attribute, ok := item.(map[string]any)
		if !ok {
			return nil, errors.New("an attribute is not an object")
		}
		key, ok := attribute["key"].(string)
		if !ok {
			return nil, errors.New("an attribute has no key")
		}
		if strings.HasPrefix(key, relayIdentityPrefix) {
			continue
		}
		kept = append(kept, attribute)
	}
	return kept, nil
}

func stringAttribute(key, value string) map[string]any {
	return map[string]any{"key": key, "value": map[string]any{"stringValue": value}}
}

// object is holder[name] as an object, made empty when it is missing.
func object(holder map[string]any, name string) (map[string]any, error) {
	raw, present := holder[name]
	if !present || raw == nil {
		created := map[string]any{}
		holder[name] = created
		return created, nil
	}
	found, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s is not an object", name)
	}
	return found, nil
}

// objects is holder[name] as a list of objects; missing is none.
func objects(holder map[string]any, name string) ([]map[string]any, error) {
	raw, present := holder[name]
	if !present || raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("%s is not a list", name)
	}
	found := make([]map[string]any, 0, len(list))
	for _, item := range list {
		entry, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("an entry of %s is not an object", name)
		}
		found = append(found, entry)
	}
	return found, nil
}

// refuseSpellings fails when holder has any of names: another spelling of
// a field the relay walks, which it would walk past.
func refuseSpellings(holder map[string]any, names ...string) error {
	for _, name := range names {
		if _, present := holder[name]; present {
			return fmt.Errorf("field %s is refused; the relay reads %s", name, "lowerCamelCase OTLP/JSON")
		}
	}
	return nil
}
