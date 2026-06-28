package menusearch

import (
	"testing"
)

func TestDedup_DeduplicatesPreservesOrder(t *testing.T) {
	input := []string{
		"https://a.com",
		"https://b.com",
		"https://a.com",
		"https://c.com",
	}
	got := dedup(input)
	want := []string{"https://a.com", "https://b.com", "https://c.com"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestDedup_Empty(t *testing.T) {
	got := dedup(nil)
	if len(got) != 0 {
		t.Errorf("dedup(nil) = %v, want empty", got)
	}
}

func TestIsDeliveryURL(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"https://myrestaurant.com/menu", false},
		{"https://yelp.com/biz/my-restaurant", true},
		{"https://doordash.com/store/my-restaurant", true},
		{"https://ubereats.com/restaurant/my-restaurant", true},
		{"https://grubhub.com/restaurant/my-restaurant", true},
		{"https://seamless.com/menus/my-restaurant", true},
		{"https://tripadvisor.com/Restaurant_Review-my-restaurant", true},
		{"https://facebook.com/myrestaurant", true},
		{"https://instagram.com/myrestaurant", true},
		{"https://google.com/maps/place/...", true},
	}
	for _, tc := range cases {
		if got := isDeliveryURL(tc.url); got != tc.want {
			t.Errorf("isDeliveryURL(%q) = %v, want %v", tc.url, got, tc.want)
		}
	}
}
