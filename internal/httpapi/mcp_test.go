package httpapi

import (
	"reflect"
	"strings"
	"testing"
)

// TestSendMessageDestinationSchema pins what send_message's schema tells a
// loop about control_room. Every user of the hub reads it (ADR-0048), and
// the loop reads this text whenever it picks a destination, so the schema
// must not call the thread private while the prompt says otherwise.
func TestSendMessageDestinationSchema(t *testing.T) {
	field, ok := reflect.TypeOf(sendMessageIn{}).FieldByName("Destination")
	if !ok {
		t.Fatal("sendMessageIn has no Destination field")
	}
	schema := field.Tag.Get("jsonschema")
	if want := "control_room (your thread in the Spool web UI, which every user of this hub reads)"; !strings.Contains(schema, want) {
		t.Fatalf("destination schema lacks %q:\n%s", want, schema)
	}
	if strings.Contains(schema, "private web thread") {
		t.Fatalf("destination schema still calls control_room private:\n%s", schema)
	}
}
