package acceptance

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

// Коды пунктов листа заказчика (Q1…Q5) — в комментариях к тестам.

const (
	day       = "2027-11-10"
	dayFrom   = "2027-11-09"
	dayTo     = "2027-11-11"
	start10   = "2027-11-10T10:00:00Z"
	end11     = "2027-11-10T11:00:00Z"
	start1030 = "2027-11-10T10:30:00Z"
	end1130   = "2027-11-10T11:30:00Z"
	end12     = "2027-11-10T12:00:00Z"
)

func sameSet(a, b []string) bool {
	return slices.Equal(slices.Sorted(slices.Values(a)), slices.Sorted(slices.Values(b)))
}

func sameInstant(t *testing.T, a, b string) bool {
	t.Helper()
	ta, err := time.Parse(time.RFC3339, a)
	if err != nil {
		t.Fatalf("parse %q: %v", a, err)
	}
	tb, err := time.Parse(time.RFC3339, b)
	if err != nil {
		t.Fatalf("parse %q: %v", b, err)
	}
	return ta.Equal(tb)
}

// Промпт: одна бронь на несколько комнат — один id, видна в выдаче каждой своей комнаты.
func TestRooms_CreatedAndListed(t *testing.T) {
	rs := roomsFor(t, 3)
	b := mustCreateRooms(t, rs[:2], start10, end11)

	if !sameSet(b.Rooms, rs[:2]) {
		t.Fatalf("rooms = %v, want %v", b.Rooms, rs[:2])
	}
	if !sameInstant(t, b.Start, start10) || !sameInstant(t, b.End, end11) {
		t.Fatalf("interval = %s–%s, want %s–%s", b.Start, b.End, start10, end11)
	}
	for _, r := range rs[:2] {
		got := listDay(t, r, day)
		if len(got) != 1 || got[0].ID != b.ID {
			t.Fatalf("GET /bookings?room=%s: %v, want exactly booking %s", r, ids(got), b.ID)
		}
		if !sameSet(got[0].Rooms, rs[:2]) {
			t.Fatalf("GET /bookings?room=%s: rooms = %v, want %v", r, got[0].Rooms, rs[:2])
		}
	}
	wantOnly(t, rs[2], dayFrom, dayTo)
}

// Q1: старый клиент шлёт room — бронь на одну эту комнату, как раньше.
func TestLegacy_OldClientRequest(t *testing.T) {
	r := room(t)
	resp := createSingle(t, r, start10, end11)
	wantStatus(t, resp, http.StatusCreated)
	b := decodeBooking(t, resp)
	if b.Room != r {
		t.Fatalf("POST: room = %q, want %q; body %s", b.Room, r, resp.body)
	}
	got := listDay(t, r, day)
	if len(got) != 1 || got[0].ID != b.ID || got[0].Room != r {
		t.Fatalf("GET /bookings?room=%s: %+v, want exactly booking %s with room %q", r, got, b.ID, r)
	}
}

// Q1: в ответах POST и GET у брони остаются и room, и rooms; room — первая из rooms,
// rooms — в порядке запроса.
func TestLegacy_ResponseKeepsRoom(t *testing.T) {
	// createShuffled создаёт бронь на три комнаты в порядке не по алфавиту
	// и возвращает порядок запроса, ответ POST и бронь из GET по средней комнате.
	createShuffled := func(t *testing.T) (order []string, posted booking, raw []byte, listed booking) {
		t.Helper()
		rs := roomsFor(t, 3)
		order = []string{rs[2], rs[0], rs[1]}
		resp := createRooms(t, order, start10, end11)
		wantStatus(t, resp, http.StatusCreated)
		got := listDay(t, rs[1], day)
		if len(got) != 1 {
			t.Fatalf("GET /bookings?room=%s: %d bookings, want 1", rs[1], len(got))
		}
		return order, decodeBooking(t, resp), resp.body, got[0]
	}

	t.Run("single room via rooms", func(t *testing.T) {
		r := room(t)
		resp := createRooms(t, []string{r}, start10, end11)
		wantStatus(t, resp, http.StatusCreated)
		if b := decodeBooking(t, resp); b.Room != r {
			t.Fatalf("POST: room = %q, want %q; body %s", b.Room, r, resp.body)
		}
		if got := listDay(t, r, day); len(got) != 1 || got[0].Room != r {
			t.Fatalf("GET: %+v, want one booking with room %q", got, r)
		}
	})
	t.Run("old client request gets rooms too", func(t *testing.T) {
		r := room(t)
		b := mustCreateSingle(t, r, start10, end11)
		if !slices.Equal(b.Rooms, []string{r}) {
			t.Fatalf("rooms = %v, want [%s]", b.Rooms, r)
		}
	})
	t.Run("room is first of rooms", func(t *testing.T) {
		order, posted, raw, listed := createShuffled(t)
		if !hasKey(t, raw, "room") || posted.Room != order[0] || listed.Room != order[0] {
			t.Fatalf("POST room = %q, GET room = %q, want %q; body %s", posted.Room, listed.Room, order[0], raw)
		}
	})
	t.Run("rooms keep request order", func(t *testing.T) {
		order, posted, _, listed := createShuffled(t)
		if !slices.Equal(posted.Rooms, order) || !slices.Equal(listed.Rooms, order) {
			t.Fatalf("POST rooms = %v, GET rooms = %v, want %v", posted.Rooms, listed.Rooms, order)
		}
	})
}

// Q1: room и rooms в одном запросе — 400, ничего не создаётся.
func TestLegacy_RoomAndRoomsTogether(t *testing.T) {
	rs := roomsFor(t, 2)
	resp := post(t, "/bookings", map[string]any{"room": rs[0], "rooms": []string{rs[1]}, "start": start10, "end": end11})
	wantStatus(t, resp, http.StatusBadRequest)
	for _, r := range rs {
		wantOnly(t, r, dayFrom, dayTo)
	}
}

// Q1: брони старых и новых клиентов конфликтуют как обычные брони.
func TestLegacy_ConflictWithMultiRoomBooking(t *testing.T) {
	tests := []struct {
		name       string
		start, end string
		wantStatus int
	}{
		{"overlap", start1030, end1130, http.StatusConflict},
		{"touching", end11, end12, http.StatusCreated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rs := roomsFor(t, 2)
			seed := mustCreateRooms(t, rs, start10, end11)
			resp := createSingle(t, rs[1], tt.start, tt.end)
			wantStatus(t, resp, tt.wantStatus)
			if tt.wantStatus != http.StatusCreated {
				wantOnly(t, rs[1], dayFrom, dayTo, seed)
			}
		})
	}
}

// Q2: хотя бы одна комната занята — 409 на всю бронь, ни в одной комнате ничего не создано.
func TestRooms_Conflict_WholeBookingRejected(t *testing.T) {
	tests := []struct {
		name       string
		seedRoom   int
		seedStart  string
		seedEnd    string
		wantStatus int
	}{
		{"first room busy", 0, start1030, end1130, http.StatusConflict},
		{"middle room busy", 1, start1030, end1130, http.StatusConflict},
		{"last room busy", 2, start1030, end1130, http.StatusConflict},
		{"touching is not a conflict", 1, end11, end12, http.StatusCreated},
		{"busy room not requested", 3, start10, end11, http.StatusCreated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rs := roomsFor(t, 4)
			seed := mustCreateRooms(t, rs[tt.seedRoom:tt.seedRoom+1], tt.seedStart, tt.seedEnd)

			resp := createRooms(t, rs[:3], start10, end11)
			wantStatus(t, resp, tt.wantStatus)
			var created []booking
			if tt.wantStatus == http.StatusCreated {
				created = []booking{decodeBooking(t, resp)}
			}
			for i, r := range rs[:3] {
				want := created
				if i == tt.seedRoom {
					want = append(slices.Clone(created), seed)
				}
				wantOnly(t, r, dayFrom, dayTo, want...)
			}
		})
	}
}

// Q3: комната дважды в rooms — 400; комнаты сравниваются точно, с учётом регистра.
func TestRooms_DuplicateRooms(t *testing.T) {
	tests := []struct {
		name       string
		rooms      func(a, b string) []string
		wantStatus int
	}{
		{"same room twice", func(a, _ string) []string { return []string{a, a} }, http.StatusBadRequest},
		{"repeat among others", func(a, b string) []string { return []string{a, b, a} }, http.StatusBadRequest},
		{"different case is a different room", func(a, _ string) []string { return []string{a, strings.ToUpper(a)} }, http.StatusCreated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rs := roomsFor(t, 2)
			resp := createRooms(t, tt.rooms(rs[0], rs[1]), start10, end11)
			wantStatus(t, resp, tt.wantStatus)
			if tt.wantStatus != http.StatusCreated {
				wantOnly(t, rs[0], dayFrom, dayTo)
				wantOnly(t, rs[1], dayFrom, dayTo)
			}
		})
	}
}

// Q4: не больше 5 комнат в одной брони.
func TestRooms_Limit(t *testing.T) {
	tests := []struct {
		name       string
		n          int
		wantStatus int
	}{
		{"five rooms", 5, http.StatusCreated},
		{"six rooms", 6, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rs := roomsFor(t, tt.n)
			resp := createRooms(t, rs, start10, end11)
			wantStatus(t, resp, tt.wantStatus)
			if tt.wantStatus == http.StatusCreated {
				b := decodeBooking(t, resp)
				for _, r := range rs {
					wantOnly(t, r, dayFrom, dayTo, b)
				}
				return
			}
			for _, r := range rs {
				wantOnly(t, r, dayFrom, dayTo)
			}
		})
	}
}

// Q5: невалидный rooms — 400, ничего не создаётся.
func TestRooms_InvalidRequest(t *testing.T) {
	tests := []struct {
		name string
		body func(r string) map[string]any
	}{
		{"empty rooms", func(string) map[string]any { return map[string]any{"rooms": []string{}} }},
		{"rooms is null", func(string) map[string]any { return map[string]any{"rooms": nil} }},
		{"rooms is a string", func(r string) map[string]any { return map[string]any{"rooms": r} }},
		{"number in rooms", func(r string) map[string]any { return map[string]any{"rooms": []any{r, 5}} }},
		{"blank room in rooms", func(r string) map[string]any { return map[string]any{"rooms": []string{r, "   "}} }},
		{"neither room nor rooms", func(string) map[string]any { return map[string]any{} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := room(t)
			body := tt.body(r)
			body["start"], body["end"] = start10, end11
			wantStatus(t, post(t, "/bookings", body), http.StatusBadRequest)
			wantOnly(t, r, dayFrom, dayTo)
		})
	}
}
