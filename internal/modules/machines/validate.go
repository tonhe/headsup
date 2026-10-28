package machines

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

var (
	dnsLabelRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	tagRe      = regexp.MustCompile(`^tag:[a-z0-9-]+$`)
	notLabelRe = regexp.MustCompile(`[^a-z0-9-]+`)
)

// ValidateName enforces headscale's given-name rule: a DNS label, lowercase
// [a-z0-9-], 1-63 characters, not starting or ending with a hyphen.
func ValidateName(s string) error {
	if dnsLabelRe.MatchString(s) {
		return nil
	}
	if sug := SanitizeName(s); sug != "" && sug != s {
		return fmt.Errorf("must be a DNS label (lowercase a-z, 0-9, -); try %q", sug)
	}
	return fmt.Errorf("must be a DNS label: lowercase a-z, 0-9 and -, 1-63 characters")
}

// SanitizeName turns arbitrary text into a valid DNS label, or "" if nothing remains.
func SanitizeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.NewReplacer(" ", "-", "_", "-", ".", "-").Replace(s)
	s = notLabelRe.ReplaceAllString(s, "")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	s = strings.Trim(s, "-")
	if len(s) > 63 {
		s = strings.TrimRight(s[:63], "-")
	}
	return s
}

// ParseTags splits a comma/space separated list, adds the "tag:" prefix where
// missing, lowercases, de-duplicates, and validates each tag.
func ParseTags(s string) ([]string, error) {
	var out []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' }) {
		t := strings.ToLower(strings.TrimSpace(f))
		if t == "" {
			continue
		}
		if !strings.HasPrefix(t, "tag:") {
			t = "tag:" + t
		}
		if !tagRe.MatchString(t) {
			return nil, fmt.Errorf("invalid tag %q: tags look like tag:name (lowercase a-z, 0-9, -)", t)
		}
		if !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out, nil
}

// MergeTags applies a bulk edit to one node's existing tags. SetTags replaces
// the whole list on the server, so bulk add/remove must merge per node.
func MergeTags(existing, add, remove []string) []string {
	out := make([]string, 0, len(existing)+len(add))
	for _, t := range existing {
		if !slices.Contains(remove, t) && !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	for _, t := range add {
		if !slices.Contains(remove, t) && !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	slices.Sort(out)
	return out
}
