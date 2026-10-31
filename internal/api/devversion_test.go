package api

import "testing"

func TestDevVersionIsNewest(t *testing.T) {
	v := "v0.0.0-20261030160536-7721d8c29978" // headscale built from main
	if !IsDevVersion(v) || IsDevVersion("v0.29.6") {
		t.Fatal("IsDevVersion")
	}
	if !VersionAtLeast(v, 0, 30) || !Supported(v) {
		t.Error("a development build is newer than every release and supported")
	}
	c := CapsForVersion(v)
	if !c.Has(CapProblemJSON) || !c.Has(CapTagOnlyKeys) {
		t.Errorf("a development build gets the newest capabilities: %v", c)
	}
}
