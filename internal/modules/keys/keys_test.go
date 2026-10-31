package keys

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/store"
	"github.com/tonhe/headsup/internal/ui/uitest"
)

const selfKey = "hskey-api-f55ff16f66f4-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func setup(t *testing.T) (*Model, *testCtx) {
	t.Helper()
	c := newCtx(t)
	c.self = api.NewSecret(selfKey)
	m := New()
	m.Init(c)
	m.Update(c, store.UpdatedMsg{Slices: store.SliceAll})
	return m, c
}

func selectAPI(t *testing.T, m *Model, c *testCtx, prefix string) {
	t.Helper()
	if m.tab != tabAPI {
		m.Update(c, keyMsg("tab"))
	}
	if !m.apiTbl.SetCursorKey(prefix) {
		t.Fatalf("no API key %q", prefix)
	}
}

func selectPre(t *testing.T, m *Model, c *testCtx, id uint64) {
	t.Helper()
	if m.tab != tabPreAuth {
		m.Update(c, keyMsg("tab"))
	}
	if !m.preTbl.SetCursorKey(fmt.Sprint(id)) {
		t.Fatalf("no pre-auth key %d", id)
	}
}

// T118: expiring the API key the app itself uses warns about locking out.
func TestExpireOwnAPIKeyWarnsOfLockout(t *testing.T) {
	m, c := setup(t)

	selectAPI(t, m, c, "f55ff16f66f4")
	exp := action(t, m, "keys.expire")
	if exp.Danger != module.DangerCritical {
		t.Fatalf("expiring the app's own key must be critical, got %v", exp.Danger)
	}
	if !strings.Contains(exp.Consequence, "lock") || !strings.Contains(exp.Consequence, "this app") {
		t.Fatalf("consequence should warn about self-lockout: %q", exp.Consequence)
	}
	if del := action(t, m, "keys.delete"); !strings.Contains(del.Consequence, "lock") {
		t.Fatalf("deleting the app's own key should warn too: %q", del.Consequence)
	}
	if v := m.View(c, module.Rect{W: 120, H: 20}); !strings.Contains(v, "this app") {
		t.Fatalf("the list should mark the app's own key:\n%s", v)
	}

	// Another key: an ordinary confirm, no lockout text.
	selectAPI(t, m, c, "2c3a4249d770")
	other := action(t, m, "keys.expire")
	if other.Danger != module.DangerConfirm || strings.Contains(other.Consequence, "lock") {
		t.Fatalf("other key: danger %v, consequence %q", other.Danger, other.Consequence)
	}
	res := run(t, c, other, nil)
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if res.Refresh&store.SliceAPIKeys == 0 {
		t.Fatal("expire should refresh API keys")
	}
	var got []any
	for _, call := range c.b.Calls() {
		if call.Method == "ExpireAPIKey" {
			got = call.Args
		}
	}
	if len(got) != 1 || got[0] != "2c3a4249d770" {
		t.Fatalf("ExpireAPIKey args = %v, want the prefix", got)
	}
}

func TestSelfDetection(t *testing.T) {
	tests := []struct {
		listed, self string
		want         bool
	}{
		{"f55ff16f66f4", "hskey-api-f55ff16f66f4-secret", true},
		{"hskey-api-f55ff16f66f4", "hskey-api-f55ff16f66f4-secret", true},
		{"abcdefghij", "abcdefghij.longsecretpart", true},
		{"f55ff16f66f4", "hskey-api-000000000000-secret", false},
		{"f55ff16f66f4", "", false},
		{"", "hskey-api-f55ff16f66f4-secret", false},
	}
	for _, tt := range tests {
		if got := IsSelf(tt.listed, PrefixOf(tt.self)); got != tt.want {
			t.Errorf("IsSelf(%q, PrefixOf(%q)) = %v, want %v", tt.listed, tt.self, got, tt.want)
		}
	}
	// Prefixes may contain '-' (URL-safe alphabet), so the new format is cut by length.
	if got := PrefixOf("hskey-api-ab-cd_ef-gh1-rest"); got != "ab-cd_ef-gh1" {
		t.Errorf("PrefixOf dash prefix = %q", got)
	}
	if got := PrefixOf("garbage"); got != "" {
		t.Errorf("PrefixOf(garbage) = %q", got)
	}
}

func TestNoSelfKnown(t *testing.T) {
	c := newCtx(t)
	m := New()
	m.Init(c)
	m.Update(c, store.UpdatedMsg{Slices: store.SliceAll})
	selectAPI(t, m, c, "f55ff16f66f4")
	if a := action(t, m, "keys.expire"); a.Danger != module.DangerConfirm {
		t.Fatalf("without a known self key, expire is an ordinary confirm: %v", a.Danger)
	}
}

func TestExpireAndDeletePreAuthKey(t *testing.T) {
	m, c := setup(t)
	selectPre(t, m, c, 1)
	exp := action(t, m, "keys.expire")
	if ok, why := exp.Enabled(c); !ok {
		t.Fatal(why)
	}
	if tg := exp.Targets(c); len(tg) != 1 || tg[0].Kind != "preauthkey" || strings.Contains(tg[0].Name, "caf7ee8e690491a9") {
		t.Fatalf("targets must name the key masked: %+v", tg)
	}
	if res := run(t, c, exp, nil); res.Err != nil {
		t.Fatal(res.Err)
	}
	c.reload()
	m.Update(c, store.UpdatedMsg{Slices: store.SlicePreAuthKeys})
	for _, k := range c.st.PreAuthKeys {
		if k.ID == 1 && k.State(testNow.Add(time.Second)) != api.KeyExpired {
			t.Fatalf("key 1 state = %v, want expired", k.State(testNow))
		}
	}
	selectPre(t, m, c, 6) // already expired in the seed
	if ok, _ := action(t, m, "keys.expire").Enabled(c); ok {
		t.Fatal("expire should be disabled for an expired key")
	}

	del := action(t, m, "keys.delete")
	if del.Cap != api.CapPreAuthKeyDelete || del.Danger != module.DangerCritical {
		t.Fatalf("pre-auth delete: cap %v danger %v", del.Cap, del.Danger)
	}
	if res := run(t, c, del, nil); res.Err != nil {
		t.Fatal(res.Err)
	}
	c.reload()
	for _, k := range c.st.PreAuthKeys {
		if k.ID == 6 {
			t.Fatal("key 6 was not deleted")
		}
	}

	selectAPI(t, m, c, "2c3a4249d770")
	if del := action(t, m, "keys.delete"); del.Cap != api.CapAPIKeyDelete {
		t.Fatalf("API delete cap = %v", del.Cap)
	}
}

func TestCreatePreAuthKeyRevealsOnce(t *testing.T) {
	m, c := setup(t)
	nk := action(t, m, "keys.new")
	spec := nk.Params(c)
	if spec.Fields[0].ID != "user" || len(spec.Fields[0].Options) != 4 {
		t.Fatalf("pre-auth form = %+v", spec.Fields[0])
	}
	msg := nk.Run(c, module.Values{"user": "carol", "reusable": "true", "expiration": "1h", "tags": "server"})()
	_, cmd := m.Update(c, msg)
	if m.reveal == nil {
		t.Fatal("a new key should be revealed")
	}
	res := cmd().(module.ActionResult)
	if res.Err != nil || !res.Silent || res.Refresh&store.SlicePreAuthKeys == 0 {
		t.Fatalf("result = %+v", res)
	}
	key := m.reveal.key.Reveal()
	v := m.View(c, module.Rect{W: 160, H: 30})
	if !strings.Contains(v, key) || !strings.Contains(v, "tailscale up --login-server=https://hs.example.com --authkey="+key) {
		t.Fatalf("reveal should show the key and command:\n%s", v)
	}
	narrow := m.View(c, module.Rect{W: 58, H: 21})
	if w := uitest.MaxWidth(narrow); w > 58 || !strings.Contains(strings.ReplaceAll(narrow, " ", ""), key[len(key)-8:]) {
		t.Fatalf("narrow reveal must wrap the key, not clip it (width %d):\n%s", w, narrow)
	}
	if ok, _ := action(t, m, "keys.expire").Enabled(c); ok {
		t.Fatal("actions on the list are disabled while a key is shown")
	}
	run(t, c, action(t, m, "keys.qr"), nil)
	if !m.reveal.showQR {
		t.Fatal("Q should toggle the QR code")
	}
	_ = m.View(c, module.Rect{W: 120, H: 60})
	m.Update(c, keyMsg("esc"))
	if m.reveal != nil {
		t.Fatal("esc should close the reveal")
	}
	c.reload()
	m.Update(c, store.UpdatedMsg{Slices: store.SlicePreAuthKeys})
	if v := m.View(c, module.Rect{W: 140, H: 30}); strings.Contains(v, key) {
		t.Fatal("after closing, the key must only appear masked")
	}
}

func TestCreateAPIKeyRevealsOnce(t *testing.T) {
	m, c := setup(t)
	m.Update(c, keyMsg("tab"))
	nk := action(t, m, "keys.new")
	spec := nk.Params(c)
	if len(spec.Fields) != 1 || spec.Fields[0].ID != "expiration" {
		t.Fatalf("API key form = %+v", spec)
	}
	msg := nk.Run(c, module.Values{"expiration": "90d"})()
	m.Update(c, msg)
	if m.reveal == nil || !m.reveal.api {
		t.Fatal("a new API key should be revealed")
	}
	v := m.View(c, module.Rect{W: 120, H: 30})
	if !strings.Contains(v, m.reveal.key.Reveal()) || strings.Contains(v, "tailscale up") {
		t.Fatalf("API reveal:\n%s", v)
	}
	if ok, _ := action(t, m, "keys.copycmd").Enabled(c); ok {
		t.Fatal("there is no tailscale up command for an API key")
	}
	if ok, _ := action(t, m, "keys.copykey").Enabled(c); !ok {
		t.Fatal("copy key should work in the reveal")
	}

	// Bad duration surfaces as an error, without a reveal.
	m.reveal = nil
	_, cmd := m.Update(c, nk.Run(c, module.Values{"expiration": "soon"})())
	if res := cmd().(module.ActionResult); res.Err == nil || m.reveal != nil {
		t.Fatalf("bad duration: %+v", res)
	}
}

// T120: the "expires in" colour ramp.
func TestExpiryRamp(t *testing.T) {
	tests := []struct {
		d    time.Duration
		zero bool
		want band
	}{
		{zero: true, want: bandNever},
		{d: -time.Hour, want: bandExpired},
		{d: 2 * time.Hour, want: bandCritical},
		{d: 3 * 24 * time.Hour, want: bandSoon},
		{d: 20 * 24 * time.Hour, want: bandNear},
		{d: 90 * 24 * time.Hour, want: bandFine},
	}
	for _, tt := range tests {
		exp := testNow.Add(tt.d)
		if tt.zero {
			exp = time.Time{}
		}
		if got := rampBand(exp, testNow); got != tt.want {
			t.Errorf("rampBand(%v) = %v, want %v", tt.d, got, tt.want)
		}
	}
}

func TestBadgeCountsExpiring(t *testing.T) {
	m, c := setup(t)
	// Seed: pre-auth 1 (24h), 2 (72h), 3 (144h) and API key 2 (240h is outside 7d).
	if got := m.Badge(c.st); got != "3" {
		t.Fatalf("badge = %q, want 3", got)
	}
	if got := New().Badge(store.New()); got != "" {
		t.Fatalf("empty badge = %q", got)
	}
}

func TestFilterByUser(t *testing.T) {
	m, c := setup(t)
	m.Update(c, keyMsg("tab")) // to API keys
	m.Update(c, module.NavigateMsg{Args: module.FilterArgs{Query: "user:bob"}})
	if m.tab != tabPreAuth {
		t.Fatal("a user filter should open the pre-auth tab")
	}
	if n := m.preTbl.Len(); n != 2 {
		t.Fatalf("bob has %d keys shown, want 2", n)
	}
	m.Update(c, module.NavigateMsg{Args: module.FilterArgs{Query: "is:expired"}})
	if n := m.preTbl.Len(); n != 1 {
		t.Fatalf("expired keys = %d, want 1", n)
	}
	m.Update(c, keyMsg("esc"))
	if m.bar.Value() != "" {
		t.Fatal("esc clears the filter")
	}
	// Typing "/" opens the bar, which then captures keys.
	m.Update(c, keyMsg("/"))
	if !m.Capturing() {
		t.Fatal("open filter bar should capture keys")
	}
}

func TestViewGolden(t *testing.T) {
	for _, tab := range []string{"preauth", "api"} {
		for _, sz := range [][2]int{{98, 37}, {58, 21}} {
			t.Run(fmt.Sprintf("%s_%dx%d", tab, sz[0], sz[1]), func(t *testing.T) {
				m, c := setup(t)
				if tab == "api" {
					m.Update(c, keyMsg("tab"))
				}
				out := m.View(c, module.Rect{W: sz[0], H: sz[1]})
				if w := uitest.MaxWidth(out); w > sz[0] {
					t.Fatalf("view is %d wide, want <= %d", w, sz[0])
				}
				if h := len(uitest.Lines(out)); h > sz[1] {
					t.Fatalf("view is %d tall, want <= %d", h, sz[1])
				}
				uitest.Golden(t, out)
			})
		}
	}
}

func TestLoadingAndHints(t *testing.T) {
	c := newCtx(t)
	m := New()
	m.Init(c)
	if v := m.View(c, module.Rect{W: 80, H: 20}); !strings.Contains(v, "Loading") {
		t.Fatalf("want loading:\n%s", v)
	}
	if len(m.Hints()) == 0 {
		t.Fatal("hints")
	}
}
