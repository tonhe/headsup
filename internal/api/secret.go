package api

// Secret holds a credential. Its String and GoString forms are masked so it
// is safe to pass to fmt, loggers, and toasts. Use Reveal deliberately.
type Secret struct {
	v string
}

// NewSecret wraps a raw credential.
func NewSecret(v string) Secret { return Secret{v: v} }

// Reveal returns the raw value. Call only when showing it to the user once
// or writing it to a protected destination.
func (s Secret) Reveal() string { return s.v }

// IsZero reports whether no secret is held.
func (s Secret) IsZero() bool { return s.v == "" }

// Masked keeps a recognisable prefix and the last four characters.
func (s Secret) Masked() string {
	if s.v == "" {
		return ""
	}
	r := []rune(s.v)
	if len(r) <= 8 {
		return "••••"
	}
	prefix := 0
	for i, c := range r {
		if c == '-' || c == '_' {
			prefix = i + 1
		}
		if i >= 11 {
			break
		}
	}
	return string(r[:prefix]) + "…" + string(r[len(r)-4:])
}

func (s Secret) String() string   { return s.Masked() }
func (s Secret) GoString() string { return "api.Secret(" + s.Masked() + ")" }

// MarshalText masks the value so secrets never leak through encoders.
func (s Secret) MarshalText() ([]byte, error) { return []byte(s.Masked()), nil }
