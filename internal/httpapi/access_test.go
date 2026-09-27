package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

// An access frame is the sender that changed, with its surface beside its
// own fields: the Access page keys its two lists on it (#230).
func TestAccessFramesNameTheirSurface(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		frame   any
		surface string
		idField string
	}{
		{"telegram", (&store.TGSender{TGUserID: 7}).Frame(), "telegram", "tg_user_id"},
		{"slack", (&store.SlackSender{SlackUserID: "U0ALICE"}).Frame(), "slack", "slack_user_id"},
	} {
		encoded, err := json.Marshal(testCase.frame)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(encoded, &fields); err != nil {
			t.Fatal(err)
		}
		if fields["surface"] != testCase.surface || fields[testCase.idField] == nil {
			t.Errorf("%s frame = %s", testCase.name, encoded)
		}
	}
}
