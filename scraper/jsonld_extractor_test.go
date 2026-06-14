package scraper

import (
	"strings"
	"testing"
)

func TestToStringSlice(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want []string
	}{
		{
			name: "slice of strings",
			in:   []any{"flour", "water", "salt"},
			want: []string{"flour", "water", "salt"},
		},
		{
			name: "slice with non-strings skipped",
			in:   []any{"flour", 123, "water"},
			want: []string{"flour", "water"},
		},
		{
			name: "plain string",
			in:   "flour",
			want: []string{"flour"},
		},
		{
			name: "int returns nil",
			in:   42,
			want: nil,
		},
		{
			name: "nil returns nil",
			in:   nil,
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toStringSlice(tt.in)
			if len(got) != len(tt.want) {
				t.Errorf("toStringSlice(%#v) = %v, want %v", tt.in, got, tt.want)
				return
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("toStringSlice(%#v)[%d] = %q, want %q", tt.in, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestExtractJSONLD_NoJSONLD(t *testing.T) {
	html := strings.NewReader(`<html><head></head><body>No menu here</body></html>`)
	items, meta, ok := ExtractJSONLD(html)
	if ok {
		t.Error("expected no JSON-LD extraction")
	}
	if len(items) != 0 {
		t.Errorf("expected no items, got %d", len(items))
	}
	if meta.RestaurantName != "" {
		t.Errorf("expected empty restaurant name, got %q", meta.RestaurantName)
	}
}

func TestExtractJSONLD_Menu(t *testing.T) {
	html := strings.NewReader(`<html><head>
<script type="application/ld+json">
{
  "@context": "https://schema.org",
  "@type": "Menu",
  "name": "Cafe Menu",
  "hasMenuItem": [
    {
      "@type": "MenuItem",
      "name": "Pancakes",
      "description": "Fluffy pancakes with maple syrup",
      "recipeIngredient": ["flour", "milk", "eggs"]
    }
  ]
}
</script>
</head><body></body></html>`)

	items, meta, ok := ExtractJSONLD(html)
	if !ok {
		t.Fatal("expected JSON-LD extraction")
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].DishName != "Pancakes" {
		t.Errorf("DishName: got %q, want %q", items[0].DishName, "Pancakes")
	}
	if !items[0].HasFullIngredients {
		t.Error("expected HasFullIngredients true because description is present")
	}
	if len(items[0].StatedIngredients) != 3 {
		t.Errorf("expected 3 ingredients, got %d", len(items[0].StatedIngredients))
	}
	if meta.RestaurantName != "" {
		t.Errorf("Menu-typed nodes do not carry restaurant metadata; got %q", meta.RestaurantName)
	}
}
