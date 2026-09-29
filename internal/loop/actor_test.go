package loop

import (
	"reflect"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

func TestLatestTickOnly(t *testing.T) {
	tick := func(text string) Envelope { return Envelope{Trigger: store.TriggerTick, Text: text} }
	message := func(text string) Envelope { return Envelope{Trigger: store.TriggerMessage, Text: text} }
	for _, testCase := range []struct {
		name string
		envs []Envelope
		want []Envelope
	}{
		{"no ticks", []Envelope{message("a"), message("b")}, []Envelope{message("a"), message("b")}},
		{"one tick", []Envelope{message("a"), tick("t1")}, []Envelope{message("a"), tick("t1")}},
		{"the latest, where the first stood",
			[]Envelope{message("a"), tick("t1"), message("b"), tick("t2"), tick("t3"), message("c")},
			[]Envelope{message("a"), tick("t3"), message("b"), message("c")}},
	} {
		if got := latestTickOnly(testCase.envs); !reflect.DeepEqual(got, testCase.want) {
			t.Errorf("%s: got %+v, want %+v", testCase.name, got, testCase.want)
		}
	}
}
