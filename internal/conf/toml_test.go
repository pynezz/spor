package conf

import "testing"

func TestParse(t *testing.T) {
	v, err := Parse([]byte(`# c
title = "Neon \"Fjord\"" # trailing
lit = 'C:\path'
n = 1_000
on = true
`))
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := v.String("title", ""); s != `Neon "Fjord"` {
		t.Fatalf("title %q", s)
	}
	if s, _ := v.String("lit", ""); s != `C:\path` {
		t.Fatalf("lit %q", s)
	}
	if n, _ := v.Int("n", 0); n != 1000 {
		t.Fatal(n)
	}
	if b, _ := v.Bool("on", false); !b {
		t.Fatal("bool")
	}
	for _, bad := range []string{"[t]", "a = ", "a = \"x", "a = 1\na = 2", "a = \"x\" y"} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	q := Quote("a\"b\\c\n")
	if v, err := Parse([]byte("k = " + q)); err != nil || v["k"] != "a\"b\\c\n" {
		t.Fatalf("quote roundtrip %v %v", v, err)
	}
}
