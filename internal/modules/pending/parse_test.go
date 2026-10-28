package pending

import "testing"

func TestParse(t *testing.T) {
	const key = "mkey:3f2a9c0d1e4b5a6c7d8e9f00"
	tests := []struct {
		name string
		in   string
		kind KeyKind
		want string
		err  bool
	}{
		{"register url", "https://hs.example.com/register/" + key, KindRegisterKey, key, false},
		{"auth url", "https://hs.example.com/auth/AbCdEf123456", KindAuthID, "AbCdEf123456", false},
		{"url with surrounding text", "To authenticate, visit:\n\n\thttps://hs.example.com/register/" + key + "\n", KindRegisterKey, key, false},
		{"url split across lines", "https://hs.example.com/regis\nter/" + key, KindRegisterKey, key, false},
		{"cli instruction", "headscale nodes register --user USERNAME --key " + key, KindRegisterKey, key, false},
		{"bare key with whitespace", "  " + key + "\n", KindRegisterKey, key, false},
		{"quoted url", `"https://hs.example.com/register/` + key + `"`, KindRegisterKey, key, false},
		{"empty", "  \n ", 0, "", true},
		{"garbage", "hello world", 0, "", true},
		{"short token", "abc", 0, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.in)
			if (err != nil) != tt.err {
				t.Fatalf("err = %v, want err %v", err, tt.err)
			}
			if tt.err {
				return
			}
			if got.Kind != tt.kind || got.Value != tt.want {
				t.Errorf("got %+v, want kind %v value %q", got, tt.kind, tt.want)
			}
		})
	}
}

func TestTailscaleUpCommand(t *testing.T) {
	got := TailscaleUpCommand("https://hs.example.com", "hskey-auth-x")
	want := "tailscale up --login-server=https://hs.example.com --authkey=hskey-auth-x"
	if got != want {
		t.Errorf("got %q", got)
	}
}
