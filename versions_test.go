package main

import "testing"

func TestIsPrerelease(t *testing.T) {
	for v, want := range map[string]bool{
		"1.2.3": false, "1.0.0+abc": false, "2.0.0+incompatible": false,
		"1.2.0-beta.1": true, "1.2.0rc1": true, "2.0.dev3": true, "1.0.0-rc.1+abc": true,
	} {
		if got := isPrerelease(v); got != want {
			t.Errorf("isPrerelease(%q) = %v", v, got)
		}
	}
}
