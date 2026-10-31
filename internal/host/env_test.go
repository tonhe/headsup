package host

import "testing"

func TestHostPath(t *testing.T) {
	e := Env{Mounts: []Mount{
		{Source: "/srv/hs/config", Destination: "/etc/headscale"},
		{Source: "/srv/hs/data", Destination: "/var/lib/headscale"},
		{Source: "/srv/hs/data/records", Destination: "/var/lib/headscale/records"},
	}}
	for in, want := range map[string]string{
		"/etc/headscale/extra-records.json": "/srv/hs/config/extra-records.json",
		"/var/lib/headscale/db.sqlite":      "/srv/hs/data/db.sqlite",
		"/var/lib/headscale/records/a.json": "/srv/hs/data/records/a.json", // longest mount wins
		"/etc/headscale":                    "/srv/hs/config",
	} {
		if got, ok := e.HostPath(in); !ok || got != want {
			t.Errorf("HostPath(%s) = %s, %v; want %s", in, got, ok, want)
		}
	}
	if got, ok := e.HostPath("/etc/headscale-other/x"); ok || got != "/etc/headscale-other/x" {
		t.Errorf("a path outside every mount is unchanged: %s %v", got, ok)
	}
}
