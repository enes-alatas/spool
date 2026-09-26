package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

// A write whose subject vanished between the lookup and the write asked for
// something that is not there: 404. Only the store itself failing is 500,
// and a wrapped ErrNotFound is still ErrNotFound (#180).
func TestStoreErrMapsNotFound(t *testing.T) {
	cases := []struct {
		err  error
		code int
		body string
	}{
		{store.ErrNotFound, http.StatusNotFound, "loop not found"},
		{fmt.Errorf("edit loop_x: %w", store.ErrNotFound), http.StatusNotFound, "loop not found"},
		{errors.New("disk I/O error"), http.StatusInternalServerError, "disk I/O error"},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		(&Server{}).storeErr(rec, tc.err, "loop")
		if rec.Code != tc.code || !strings.Contains(rec.Body.String(), tc.body) {
			t.Errorf("storeErr(%v) = %d %s, want %d naming %q", tc.err, rec.Code, rec.Body, tc.code, tc.body)
		}
	}
}
