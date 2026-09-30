package acceptance

import (
	"net/http"
	"testing"
)

// Регрессия базового поведения: запросы старого клиента (поле room) работают как раньше.
func TestBase_SingleBooking(t *testing.T) {
	tests := []struct {
		name       string
		start, end string
		wantStatus int
	}{
		{"free slot", "2027-11-09T13:00:00Z", "2027-11-09T14:00:00Z", http.StatusCreated},
		{"overlap", "2027-11-09T10:30:00Z", "2027-11-09T11:30:00Z", http.StatusConflict},
		{"touching end of existing", "2027-11-09T11:00:00Z", "2027-11-09T12:00:00Z", http.StatusCreated},
		{"touching start of existing", "2027-11-09T09:00:00Z", "2027-11-09T10:00:00Z", http.StatusCreated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := room(t)
			seed := mustCreateSingle(t, r, "2027-11-09T10:00:00Z", "2027-11-09T11:00:00Z")
			resp := createSingle(t, r, tt.start, tt.end)
			wantStatus(t, resp, tt.wantStatus)
			if tt.wantStatus != http.StatusCreated {
				wantOnly(t, r, "2027-11-08", "2027-11-10", seed)
			}
		})
	}
}
