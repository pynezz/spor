package server

import "testing"

func TestPickLang(t *testing.T) {
	for h, want := range map[string]string{
		"":                        "nb",
		"nb-NO,nb;q=0.9,en;q=0.8": "nb",
		"nn":                      "nb",
		"no":                      "nb",
		"en-US,en;q=0.9":          "en-US",
		"de-DE":                   "en-US",
		"en;q=0.5, nb;q=0.9":      "nb",
		"sv-SE,nb;q=0.8":          "en-US",
	} {
		if got := pickLang(h); got != want {
			t.Errorf("%q: got %s want %s", h, got, want)
		}
	}
}
