package cli

import (
	"testing"

	"github.com/spf13/viper"
)

func TestRiverSchemaName_Default(t *testing.T) {
	viper.Set("river-schema", "")
	if got := riverSchemaName(); got != "river" {
		t.Errorf("expected default 'river', got %q", got)
	}
}

func TestRiverSchemaName_FromViper(t *testing.T) {
	viper.Set("river-schema", "my_river")
	if got := riverSchemaName(); got != "my_river" {
		t.Errorf("expected 'my_river', got %q", got)
	}
	viper.Set("river-schema", "") // restore for other tests
}

func TestQuoteIdent(t *testing.T) {
	cases := []struct{ in, want string }{
		{"river", `"river"`},
		{"my_river", `"my_river"`},
		{`a"b`, `"a""b"`},
		{`a""b`, `"a""""b"`},
	}
	for _, c := range cases {
		if got := quoteIdent(c.in); got != c.want {
			t.Errorf("quoteIdent(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
