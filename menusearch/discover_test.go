package menusearch

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestRedirectClient() *http.Client {
	return &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func TestDedupAndFilter_DropsDelivery(t *testing.T) {
	input := []string{
		"https://myrestaurant.com/menu",
		"https://yelp.com/biz/my-restaurant",
		"https://doordash.com/store/my-restaurant",
		"https://myrestaurant.com/menu", // duplicate
		"https://ubereats.com/restaurant/my-restaurant",
		"https://grubhub.com/restaurant/my-restaurant",
	}
	got := dedupAndFilter(input)
	if len(got) != 1 {
		t.Fatalf("dedupAndFilter() returned %d URLs, want 1; got %v", len(got), got)
	}
	if got[0] != "https://myrestaurant.com/menu" {
		t.Errorf("got %q, want %q", got[0], "https://myrestaurant.com/menu")
	}
}

func TestDedupAndFilter_AllDelivery_ReturnsEmpty(t *testing.T) {
	input := []string{
		"https://yelp.com/biz/my-restaurant",
		"https://tripadvisor.com/Restaurant_Review-my-restaurant",
		"https://seamless.com/menus/my-restaurant",
	}
	got := dedupAndFilter(input)
	if len(got) != 0 {
		t.Errorf("dedupAndFilter() = %v, want empty", got)
	}
}

func TestDedupAndFilter_DeduplicatesPreservesOrder(t *testing.T) {
	input := []string{
		"https://a.com",
		"https://b.com",
		"https://a.com",
		"https://c.com",
	}
	got := dedupAndFilter(input)
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

func TestDedupAndFilter_Empty(t *testing.T) {
	got := dedupAndFilter(nil)
	if len(got) != 0 {
		t.Errorf("dedupAndFilter(nil) = %v, want empty", got)
	}
}

func TestResolveRedirects_FollowsRedirect(t *testing.T) {
	// Create a server that redirects /start → /final.
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer final.Close()

	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, final.URL+"/final", http.StatusFound)
	}))
	defer redirect.Close()

	got := resolveRedirects(t.Context(), newTestRedirectClient(), []string{redirect.URL + "/start"})
	if len(got) != 1 {
		t.Fatalf("resolveRedirects returned %d URLs, want 1", len(got))
	}
	if got[0] != final.URL+"/final" {
		t.Errorf("got %q, want %q", got[0], final.URL+"/final")
	}
}

func TestResolveRedirects_PassthroughNoRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	input := []string{srv.URL + "/page"}
	got := resolveRedirects(t.Context(), newTestRedirectClient(), input)
	if len(got) != 1 || got[0] != input[0] {
		t.Errorf("resolveRedirects passthrough = %v, want %v", got, input)
	}
}

func TestResolveRedirects_UnreachableHost_Passthrough(t *testing.T) {
	// An unreachable host should pass through unchanged (not crash).
	input := []string{"http://127.0.0.1:0/nowhere"}
	got := resolveRedirects(t.Context(), newTestRedirectClient(), input)
	if len(got) != 1 {
		t.Fatalf("resolveRedirects returned %d URLs, want 1", len(got))
	}
	// The URL should be the original (error on connect → no redirect followed).
	if got[0] != input[0] {
		t.Errorf("got %q, want %q (original, since unreachable)", got[0], input[0])
	}
}

func TestResolveRedirects_Empty(t *testing.T) {
	got := resolveRedirects(t.Context(), newTestRedirectClient(), nil)
	if len(got) != 0 {
		t.Errorf("resolveRedirects(nil) = %v, want empty", got)
	}
}
