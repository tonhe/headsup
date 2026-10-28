package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/tonhe/fathom/internal/api"
	"github.com/tonhe/fathom/internal/api/apitest"
	"github.com/tonhe/fathom/internal/api/fake"
)

const testKey = "hskey-api-testprefix01-0123456789abcdef"

// sim is a test headscale REST server backed by fake.Backend. v030 selects
// Huma behaviour (problem+json, expire body); otherwise grpc-gateway 0.29.
type sim struct {
	fb       *fake.Backend
	v030     bool
	noAuth   bool // local-trust (unix socket)
	requests atomic.Int64
	sawAuth  atomic.Bool
	style    apitest.Style
}

func newSim(version string) *sim {
	s := &sim{fb: fake.Demo(fake.WithVersion(version)), v030: api.VersionAtLeast(version, 0, 30)}
	s.style = apitest.StyleV029REST
	if s.v030 {
		s.style = apitest.StyleV030
	}
	return s
}

func (s *sim) writeErr(w http.ResponseWriter, err error) {
	e := api.AsError(err)
	status := map[api.ErrorKind]int{
		api.KindNotFound: 404, api.KindInvalid: 400, api.KindConflict: 409,
		api.KindUnauthorized: 401, api.KindUnsupported: 501, api.KindUnreachable: 503,
	}[e.Kind]
	if status == 0 {
		status = 500
	}
	if s.v030 {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"title": http.StatusText(status), "status": status, "detail": e.Message})
		return
	}
	code := map[int]int{404: 5, 400: 3, 409: 6, 401: 16, 501: 12, 503: 14, 500: 2}[status]
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"code": code, "message": e.Message, "details": []any{}})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func body(r *http.Request) map[string]any {
	m := map[string]any{}
	data, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(data, &m)
	return m
}

func pathID(r *http.Request, name string) uint64 {
	n, _ := strconv.ParseUint(r.PathValue(name), 10, 64)
	return n
}

func strList(v any) []string {
	arr, _ := v.([]any)
	out := []string{}
	for _, x := range arr {
		s, _ := x.(string)
		out = append(out, s)
	}
	return out
}

func parseTime(v any) time.Time {
	s, _ := v.(string)
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

func (s *sim) handler() http.Handler {
	mux := http.NewServeMux()
	st := s.style
	node := func(w http.ResponseWriter, n api.Node, err error) {
		if err != nil {
			s.writeErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"node": st.Node(n)})
	}
	empty := func(w http.ResponseWriter, err error) {
		if err != nil {
			s.writeErr(w, err)
			return
		}
		writeJSON(w, map[string]any{})
	}
	userName := func(id uint64) (string, error) {
		us, err := s.fb.ListUsers(context.Background(), api.UserFilter{ID: id})
		if err != nil || len(us) == 0 {
			return "", api.Errf(api.KindNotFound, "user not found")
		}
		return us[0].Name, nil
	}

	mux.HandleFunc("GET /version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"version": s.fb.Version(), "commit": "abc123", "dirty": false})
	})
	mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{st.Key("databaseConnectivity", "database_connectivity"): true})
	})
	mux.HandleFunc("GET /api/v1/node", func(w http.ResponseWriter, r *http.Request) {
		ns, err := s.fb.ListNodes(r.Context(), api.NodeFilter{User: r.URL.Query().Get("user")})
		if err != nil {
			s.writeErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"nodes": st.Nodes(ns)})
	})
	mux.HandleFunc("GET /api/v1/node/{id}", func(w http.ResponseWriter, r *http.Request) {
		n, err := s.fb.GetNode(r.Context(), pathID(r, "id"))
		node(w, n, err)
	})
	mux.HandleFunc("DELETE /api/v1/node/{id}", func(w http.ResponseWriter, r *http.Request) {
		empty(w, s.fb.DeleteNode(r.Context(), pathID(r, "id")))
	})
	mux.HandleFunc("POST /api/v1/node/register", func(w http.ResponseWriter, r *http.Request) {
		n, err := s.fb.RegisterNode(r.Context(), r.URL.Query().Get("user"), r.URL.Query().Get("key"))
		node(w, n, err)
	})
	mux.HandleFunc("POST /api/v1/node/backfillips", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("confirmed") != "true" {
			s.writeErr(w, api.Errf(api.KindInvalid, "not confirmed"))
			return
		}
		ch, err := s.fb.BackfillIPs(r.Context())
		if err != nil {
			s.writeErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"changes": ch})
	})
	mux.HandleFunc("POST /api/v1/node/{id}/rename/{name}", func(w http.ResponseWriter, r *http.Request) {
		n, err := s.fb.RenameNode(r.Context(), pathID(r, "id"), r.PathValue("name"))
		node(w, n, err)
	})
	mux.HandleFunc("POST /api/v1/node/{id}/tags", func(w http.ResponseWriter, r *http.Request) {
		n, err := s.fb.SetTags(r.Context(), pathID(r, "id"), strList(body(r)["tags"]))
		node(w, n, err)
	})
	mux.HandleFunc("POST /api/v1/node/{id}/approve_routes", func(w http.ResponseWriter, r *http.Request) {
		var ps []netip.Prefix
		for _, x := range strList(body(r)["routes"]) {
			p, err := netip.ParsePrefix(x)
			if err != nil {
				s.writeErr(w, api.Errf(api.KindInvalid, "parsing route: %v", err))
				return
			}
			ps = append(ps, p)
		}
		n, err := s.fb.SetApprovedRoutes(r.Context(), pathID(r, "id"), ps)
		node(w, n, err)
	})
	mux.HandleFunc("POST /api/v1/node/{id}/expire", func(w http.ResponseWriter, r *http.Request) {
		var opt api.ExpireOpts
		if s.v030 { // Huma: JSON body
			b := body(r)
			opt.DisableExpiry, _ = b["disableExpiry"].(bool)
			opt.At = parseTime(b["expiry"])
		} else { // grpc-gateway: query parameters
			q := r.URL.Query()
			opt.DisableExpiry = q.Get("disableExpiry") == "true"
			opt.At = parseTime(q.Get("expiry"))
		}
		n, err := s.fb.ExpireNode(r.Context(), pathID(r, "id"), opt)
		node(w, n, err)
	})
	mux.HandleFunc("GET /api/v1/user", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		id, _ := strconv.ParseUint(q.Get("id"), 10, 64)
		us, err := s.fb.ListUsers(r.Context(), api.UserFilter{ID: id, Name: q.Get("name"), Email: q.Get("email")})
		if err != nil {
			s.writeErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"users": st.Users(us)})
	})
	mux.HandleFunc("POST /api/v1/user", func(w http.ResponseWriter, r *http.Request) {
		b := body(r)
		str := func(k string) string { v, _ := b[k].(string); return v }
		u, err := s.fb.CreateUser(r.Context(), api.NewUser{Name: str("name"), DisplayName: str("displayName"), Email: str("email")})
		if err != nil {
			s.writeErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"user": st.User(u)})
	})
	mux.HandleFunc("POST /api/v1/user/{id}/rename/{name}", func(w http.ResponseWriter, r *http.Request) {
		u, err := s.fb.RenameUser(r.Context(), pathID(r, "id"), r.PathValue("name"))
		if err != nil {
			s.writeErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"user": st.User(u)})
	})
	mux.HandleFunc("DELETE /api/v1/user/{id}", func(w http.ResponseWriter, r *http.Request) {
		empty(w, s.fb.DeleteUser(r.Context(), pathID(r, "id")))
	})
	mux.HandleFunc("GET /api/v1/preauthkey", func(w http.ResponseWriter, r *http.Request) {
		ks, err := s.fb.ListPreAuthKeys(r.Context(), api.KeyFilter{})
		if err != nil {
			s.writeErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"preAuthKeys": st.PreAuthKeys(ks)})
	})
	mux.HandleFunc("POST /api/v1/preauthkey", func(w http.ResponseWriter, r *http.Request) {
		b := body(r)
		nk := api.NewPreAuthKey{ACLTags: strList(b["aclTags"]), Expiration: parseTime(b["expiration"])}
		nk.Reusable, _ = b["reusable"].(bool)
		nk.Ephemeral, _ = b["ephemeral"].(bool)
		if u, _ := b["user"].(string); u != "" {
			id, _ := strconv.ParseUint(u, 10, 64)
			name, err := userName(id)
			if err != nil {
				s.writeErr(w, err)
				return
			}
			nk.User = name
		}
		k, err := s.fb.CreatePreAuthKey(r.Context(), nk)
		if err != nil {
			s.writeErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"preAuthKey": st.PreAuthKey(k)})
	})
	mux.HandleFunc("POST /api/v1/preauthkey/expire", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseUint(toS(body(r)["id"]), 10, 64)
		empty(w, s.fb.ExpirePreAuthKey(r.Context(), id))
	})
	mux.HandleFunc("DELETE /api/v1/preauthkey", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseUint(r.URL.Query().Get("id"), 10, 64)
		empty(w, s.fb.DeletePreAuthKey(r.Context(), id))
	})
	mux.HandleFunc("GET /api/v1/apikey", func(w http.ResponseWriter, r *http.Request) {
		ks, err := s.fb.ListAPIKeys(r.Context())
		if err != nil {
			s.writeErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"apiKeys": st.APIKeys(ks)})
	})
	mux.HandleFunc("POST /api/v1/apikey", func(w http.ResponseWriter, r *http.Request) {
		sec, err := s.fb.CreateAPIKey(r.Context(), parseTime(body(r)["expiration"]))
		if err != nil {
			s.writeErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"apiKey": sec.Reveal()})
	})
	mux.HandleFunc("POST /api/v1/apikey/expire", func(w http.ResponseWriter, r *http.Request) {
		empty(w, s.fb.ExpireAPIKey(r.Context(), toS(body(r)["prefix"])))
	})
	mux.HandleFunc("DELETE /api/v1/apikey/{prefix}", func(w http.ResponseWriter, r *http.Request) {
		empty(w, s.fb.DeleteAPIKey(r.Context(), r.PathValue("prefix")))
	})
	policy := func(w http.ResponseWriter, p api.Policy, err error) {
		if err != nil {
			s.writeErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"policy": p.Raw, "updatedAt": st.Time(p.UpdatedAt)})
	}
	mux.HandleFunc("GET /api/v1/policy", func(w http.ResponseWriter, r *http.Request) {
		p, err := s.fb.GetPolicy(r.Context())
		policy(w, p, err)
	})
	mux.HandleFunc("PUT /api/v1/policy", func(w http.ResponseWriter, r *http.Request) {
		p, err := s.fb.SetPolicy(r.Context(), toS(body(r)["policy"]))
		policy(w, p, err)
	})
	mux.HandleFunc("POST /api/v1/policy/check", func(w http.ResponseWriter, r *http.Request) {
		empty(w, s.fb.CheckPolicy(r.Context(), toS(body(r)["policy"])))
	})
	mux.HandleFunc("POST /api/v1/auth/register", func(w http.ResponseWriter, r *http.Request) {
		b := body(r)
		empty(w, s.fb.AuthRegister(r.Context(), toS(b["authId"]), toS(b["user"])))
	})
	mux.HandleFunc("POST /api/v1/auth/approve", func(w http.ResponseWriter, r *http.Request) {
		empty(w, s.fb.AuthApprove(r.Context(), toS(body(r)["authId"])))
	})
	mux.HandleFunc("POST /api/v1/auth/reject", func(w http.ResponseWriter, r *http.Request) {
		empty(w, s.fb.AuthReject(r.Context(), toS(body(r)["authId"])))
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		if r.Header.Get("Authorization") != "" {
			s.sawAuth.Store(true)
		}
		if strings.HasPrefix(r.URL.Path, "/api/") && !s.noAuth && r.Header.Get("Authorization") != "Bearer "+testKey {
			s.writeErr(w, api.Errf(api.KindUnauthorized, "Unauthorized"))
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func toS(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	}
	return ""
}
