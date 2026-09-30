// Приёмочные тесты воркшопа. Чёрный ящик: собирают ../cmd/booking, поднимают
// бинарник на свободном порту и ходят в него по HTTP. Внутренние пакеты не импортируются,
// поэтому тесты не зависят от того, как агент устроил код.
//
// Переменные окружения:
//
//	ACCEPTANCE_RACE=0       собрать без -race (по умолчанию с ним; нужен cgo).
//	ACCEPTANCE_BASE_URL=... не собирать и не запускать, бить в уже поднятый сервис.
package acceptance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	baseURL string
	client  = &http.Client{Timeout: 30 * time.Second}
)

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestMain(m *testing.M) {
	if u := os.Getenv("ACCEPTANCE_BASE_URL"); u != "" {
		baseURL = strings.TrimSuffix(u, "/")
		os.Exit(runAgainstExternal(m))
	}
	os.Exit(runWithServer(m))
}

func runAgainstExternal(m *testing.M) int {
	if err := waitReady(3 * time.Second); err != nil {
		fmt.Fprintf(os.Stderr, "service not ready: %v — подними сервис из проверяемой рабочей копии (make run)\n", err)
		return 1
	}
	code := m.Run()
	fmt.Println("race detector: не проверялся — сервис запущен снаружи")
	return code
}

func runWithServer(m *testing.M) int {
	dir, err := os.MkdirTemp("", "acceptance")
	if err != nil {
		fmt.Fprintln(os.Stderr, "tempdir:", err)
		return 1
	}
	defer os.RemoveAll(dir)

	bin := filepath.Join(dir, "booking")
	args := []string{"build", "-o", bin}
	withRace := os.Getenv("ACCEPTANCE_RACE") != "0"
	if withRace {
		args = append(args, "-race")
	}
	build := exec.Command("go", append(args, "./cmd/booking")...)
	build.Dir = ".."
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "go build: %v\n%s", err, out)
		return 1
	}

	port, err := freePort()
	if err != nil {
		fmt.Fprintln(os.Stderr, "free port:", err)
		return 1
	}
	var stderr lockedBuffer
	srv := exec.Command(bin)
	srv.Env = append(os.Environ(), "PORT="+port)
	srv.Stdout = io.Discard
	srv.Stderr = &stderr
	if err := srv.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "start service:", err)
		return 1
	}
	defer func() {
		_ = srv.Process.Kill()
		_ = srv.Wait()
	}()

	baseURL = "http://127.0.0.1:" + port
	if err := waitReady(15 * time.Second); err != nil {
		fmt.Fprintf(os.Stderr, "service not ready: %v\nstderr:\n%s", err, stderr.String())
		return 1
	}

	code := m.Run()

	if log := stderr.String(); strings.Contains(log, "DATA RACE") {
		fmt.Fprintf(os.Stderr, "\nFAIL: race detector reported a data race in the service\n%s", log)
		return 1
	}
	if withRace {
		fmt.Println("race detector: clean")
	}
	return code
}

func freePort() (string, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer l.Close()
	_, port, err := net.SplitHostPort(l.Addr().String())
	return port, err
}

func waitReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := client.Get(baseURL + "/bookings?room=ping&date=2026-01-01")
		if err == nil {
			resp.Body.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("no response from %s", baseURL)
}

// --- HTTP ---

// booking — бронь в ответах сервиса. Room — поле старых клиентов, Rooms — новое.
type booking struct {
	ID    string   `json:"id"`
	Room  string   `json:"room"`
	Rooms []string `json:"rooms"`
	Start string   `json:"start"`
	End   string   `json:"end"`
}

type response struct {
	status int
	body   []byte
}

func post(t *testing.T, path string, body any) response {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resp, err := client.Post(baseURL+path, "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return response{status: resp.StatusCode, body: data}
}

// runID отделяет комнаты разных прогонов: внешний сервис может пережить
// несколько запусков приёмки, а хранилище у него одно.
var runID = strconv.FormatInt(time.Now().UnixNano(), 36)

// room — уникальная комната на тест и прогон.
func room(t *testing.T) string {
	return strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()) + "_" + runID
}

// roomsFor — n уникальных комнат теста: <room>_a, <room>_b, ...
func roomsFor(t *testing.T, n int) []string {
	base := room(t)
	out := make([]string, n)
	for i := range out {
		out[i] = base + "_" + string(rune('a'+i))
	}
	return out
}

// createSingle — запрос старого клиента: одна комната в поле room.
func createSingle(t *testing.T, room, start, end string) response {
	t.Helper()
	return post(t, "/bookings", map[string]any{"room": room, "start": start, "end": end})
}

func mustCreateSingle(t *testing.T, room, start, end string) booking {
	t.Helper()
	r := createSingle(t, room, start, end)
	if r.status != http.StatusCreated {
		t.Fatalf("seed booking room=%s %s–%s: status %d, body %s", room, start, end, r.status, r.body)
	}
	return decodeBooking(t, r)
}

// createRooms — запрос нового клиента: комнаты в поле rooms.
func createRooms(t *testing.T, rooms []string, start, end string) response {
	t.Helper()
	return post(t, "/bookings", map[string]any{"rooms": rooms, "start": start, "end": end})
}

func mustCreateRooms(t *testing.T, rooms []string, start, end string) booking {
	t.Helper()
	r := createRooms(t, rooms, start, end)
	if r.status != http.StatusCreated {
		t.Fatalf("POST /bookings rooms=%v %s–%s: status %d, want 201, body %s", rooms, start, end, r.status, r.body)
	}
	return decodeBooking(t, r)
}

func decodeBooking(t *testing.T, r response) booking {
	t.Helper()
	var b booking
	if err := json.Unmarshal(r.body, &b); err != nil {
		t.Fatalf("decode booking %q: %v", r.body, err)
	}
	if b.ID == "" {
		t.Fatalf("booking id is empty: %s", r.body)
	}
	return b
}

func wantStatus(t *testing.T, r response, want int) {
	t.Helper()
	if r.status != want {
		t.Fatalf("status = %d, want %d, body %s", r.status, want, r.body)
	}
	if want >= 400 {
		var e struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(r.body, &e); err != nil || e.Error == "" {
			t.Fatalf("error body %q: want {\"error\": \"...\"}", r.body)
		}
	}
}

// listRaw — сырой ответ GET /bookings за одни сутки.
func listRaw(t *testing.T, room, date string) []byte {
	t.Helper()
	q := url.Values{"room": {room}, "date": {date}}
	resp, err := client.Get(baseURL + "/bookings?" + q.Encode())
	if err != nil {
		t.Fatalf("GET /bookings: %v", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /bookings?%s: status %d, body %s", q.Encode(), resp.StatusCode, data)
	}
	return data
}

func listDay(t *testing.T, room, date string) []booking {
	t.Helper()
	data := listRaw(t, room, date)
	var page struct {
		Bookings []booking `json:"bookings"`
	}
	if err := json.Unmarshal(data, &page); err != nil {
		t.Fatalf("decode list %q: %v", data, err)
	}
	return page.Bookings
}

// listRange собирает брони комнаты за даты [from, to] включительно, без дублей
// (бронь через полночь приходит в выдаче двух дней).
func listRange(t *testing.T, room, from, to string) []booking {
	t.Helper()
	d, err := time.Parse(time.DateOnly, from)
	if err != nil {
		t.Fatalf("from: %v", err)
	}
	last, err := time.Parse(time.DateOnly, to)
	if err != nil {
		t.Fatalf("to: %v", err)
	}
	seen := map[string]bool{}
	var all []booking
	for ; !d.After(last); d = d.AddDate(0, 0, 1) {
		for _, b := range listDay(t, room, d.Format(time.DateOnly)) {
			if !seen[b.ID] {
				seen[b.ID] = true
				all = append(all, b)
			}
		}
	}
	return all
}

func ids(bs []booking) []string {
	out := make([]string, len(bs))
	for i, b := range bs {
		out[i] = b.ID
	}
	slices.Sort(out)
	return out
}

// wantOnly проверяет, что в комнате за даты [from, to] ровно брони keep, и ничего сверх.
func wantOnly(t *testing.T, room, from, to string, keep ...booking) {
	t.Helper()
	got := listRange(t, room, from, to)
	if !slices.Equal(ids(got), ids(keep)) {
		t.Fatalf("room %s has bookings %v, want only %v", room, ids(got), ids(keep))
	}
}

// hasKey — есть ли поле key в JSON-объекте.
func hasKey(t *testing.T, obj []byte, key string) bool {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(obj, &m); err != nil {
		t.Fatalf("decode object %q: %v", obj, err)
	}
	_, ok := m[key]
	return ok
}
