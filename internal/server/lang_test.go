package server

import "testing"

func TestPickLang(t *testing.T) {
	for q, want := range map[string]string{
		"":      "nb",
		"nb":    "nb",
		"de":    "nb",
		"en":    "en-US",
		"EN-us": "en-US",
		"en-gb": "en-US",
	} {
		if got := pickLang(q); got != want {
			t.Errorf("%q: got %s want %s", q, got, want)
		}
	}
}

func TestFmtDate(t *testing.T) {
	for _, c := range []struct{ lang, in, want string }{
		{"nb", "2019-03-12", "12. mars 2019"},
		{"nb", "2019-03", "mars 2019"},
		{"nb", "2019", "2019"},
		{"en-US", "2019-03-12", "March 12, 2019"},
		{"nb", "", ""},
		{"nb", "sommer 2019", "sommer 2019"},
		{"nb", "2019-13-01", "2019-13-01"},
	} {
		if got := fmtDate(c.lang, c.in); got != c.want {
			t.Errorf("fmtDate(%s, %q) = %q, want %q", c.lang, c.in, got, c.want)
		}
	}
}

func TestFooterLinks(t *testing.T) {
	c := DefaultConfig()
	c.PublicKeys = []string{"x.pub"}
	if err := c.Finish(); err != nil {
		t.Fatal(err)
	}
	if c.SunoHandle != "@skumx" {
		t.Fatalf("handle %q", c.SunoHandle)
	}
	c.ReferralURL = "http://suno.com/invite/x"
	if err := c.Finish(); err == nil {
		t.Fatal("plain http referral accepted")
	}
}
