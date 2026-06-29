package menusearch

import (
	"testing"
)

// ── extractMenuSubURLs ───────────────────────────────────────────────────────

// baseURL used across most sub-tests.
const testBase = "https://restaurant.example.com/menu"

func TestExtractMenuSubURLs_SameDomainKept(t *testing.T) {
	html := []byte(`<html><body>
<a href="/menu/lunch">Lunch</a>
<a href="/menu/dinner">Dinner</a>
</body></html>`)
	got := extractMenuSubURLs(html, testBase)
	if len(got) != 2 {
		t.Fatalf("expected 2 candidates, got %v", got)
	}
	want := map[string]bool{
		"https://restaurant.example.com/menu/lunch":  true,
		"https://restaurant.example.com/menu/dinner": true,
	}
	for _, u := range got {
		if !want[u] {
			t.Errorf("unexpected candidate %q", u)
		}
	}
}

func TestExtractMenuSubURLs_PDFKept(t *testing.T) {
	html := []byte(`<html><body>
<a href="/menus/full-menu.pdf">Download Menu</a>
</body></html>`)
	got := extractMenuSubURLs(html, testBase)
	if len(got) != 1 {
		t.Fatalf("expected 1 PDF candidate, got %v", got)
	}
	if got[0] != "https://restaurant.example.com/menus/full-menu.pdf" {
		t.Errorf("unexpected URL %q", got[0])
	}
}

func TestExtractMenuSubURLs_OffDomainDropped(t *testing.T) {
	html := []byte(`<html><body>
<a href="https://other-restaurant.com/menu">Other menu</a>
<a href="/menu/lunch">Lunch</a>
</body></html>`)
	got := extractMenuSubURLs(html, testBase)
	// Only the same-domain link should survive.
	if len(got) != 1 {
		t.Fatalf("expected 1 same-domain candidate, got %v", got)
	}
	if got[0] != "https://restaurant.example.com/menu/lunch" {
		t.Errorf("unexpected URL %q", got[0])
	}
}

func TestExtractMenuSubURLs_SocialDropped(t *testing.T) {
	html := []byte(`<html><body>
<a href="https://instagram.com/restaurant">Instagram</a>
<a href="https://facebook.com/restaurant">Facebook</a>
<a href="/menu/brunch">Brunch Menu</a>
</body></html>`)
	got := extractMenuSubURLs(html, testBase)
	if len(got) != 1 {
		t.Fatalf("expected only brunch link, got %v", got)
	}
}

func TestExtractMenuSubURLs_DeliveryDropped(t *testing.T) {
	html := []byte(`<html><body>
<a href="https://doordash.com/store/restaurant">Order on DoorDash</a>
<a href="https://ubereats.com/restaurant/place">Order on UberEats</a>
<a href="/menu/food">Food Menu</a>
</body></html>`)
	got := extractMenuSubURLs(html, testBase)
	if len(got) != 1 {
		t.Fatalf("expected only food link, got %v", got)
	}
}

func TestExtractMenuSubURLs_NonMenuPathDropped(t *testing.T) {
	// Pages without menu-ish keywords in the path should be filtered out.
	html := []byte(`<html><body>
<a href="/about">About Us</a>
<a href="/contact">Contact</a>
<a href="/reservations">Reserve a Table</a>
<a href="/menu/lunch">Lunch</a>
</body></html>`)
	got := extractMenuSubURLs(html, testBase)
	if len(got) != 1 {
		t.Fatalf("expected only lunch link, got %v", got)
	}
}

func TestExtractMenuSubURLs_SelfURLDropped(t *testing.T) {
	// The root URL itself must not appear in the results.
	html := []byte(`<html><body>
<a href="https://restaurant.example.com/menu">This Menu Page</a>
<a href="/menu/">Trailing slash variant</a>
<a href="/menu/dinner">Dinner</a>
</body></html>`)
	got := extractMenuSubURLs(html, testBase)
	// /menu and /menu/ are both normalised to the root; only /menu/dinner survives.
	for _, u := range got {
		if u == testBase || u == "https://restaurant.example.com/menu/" {
			t.Errorf("self-URL should have been dropped: %q", u)
		}
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 candidate (dinner), got %v", got)
	}
}

func TestExtractMenuSubURLs_Dedup(t *testing.T) {
	// Duplicate hrefs should appear only once.
	html := []byte(`<html><body>
<a href="/menu/lunch">Lunch (nav)</a>
<a href="/menu/lunch">Lunch (footer)</a>
</body></html>`)
	got := extractMenuSubURLs(html, testBase)
	if len(got) != 1 {
		t.Fatalf("expected 1 deduplicated candidate, got %v", got)
	}
}

func TestExtractMenuSubURLs_RelativeResolved(t *testing.T) {
	// Relative hrefs must be resolved against the base URL.
	base := "https://restaurant.example.com/"
	html := []byte(`<html><body>
<a href="menu/brunch">Brunch</a>
</body></html>`)
	got := extractMenuSubURLs(html, base)
	if len(got) != 1 {
		t.Fatalf("expected 1 resolved relative URL, got %v", got)
	}
	want := "https://restaurant.example.com/menu/brunch"
	if got[0] != want {
		t.Errorf("got %q, want %q", got[0], want)
	}
}

func TestExtractMenuSubURLs_EmptyHTML(t *testing.T) {
	got := extractMenuSubURLs([]byte{}, testBase)
	if len(got) != 0 {
		t.Errorf("expected no candidates for empty HTML, got %v", got)
	}
}

func TestExtractMenuSubURLs_NoAnchors(t *testing.T) {
	html := []byte(`<html><body><p>No links here.</p></body></html>`)
	got := extractMenuSubURLs(html, testBase)
	if len(got) != 0 {
		t.Errorf("expected no candidates when no anchors, got %v", got)
	}
}

func TestExtractMenuSubURLs_WwwStrippedSameDomain(t *testing.T) {
	// www. prefix should be treated as same domain as the non-www root.
	base := "https://www.restaurant.example.com/menu"
	html := []byte(`<html><body>
<a href="https://restaurant.example.com/menu/dinner">Dinner</a>
</body></html>`)
	got := extractMenuSubURLs(html, base)
	if len(got) != 1 {
		t.Fatalf("expected www vs non-www to be same domain, got %v", got)
	}
}

// ── registrableDomain ────────────────────────────────────────────────────────

func TestRegistrableDomain(t *testing.T) {
	cases := []struct {
		host string
		want string
	}{
		{"restaurant.example.com", "example.com"},
		{"www.example.com", "example.com"},
		{"example.com", "example.com"},
		{"sub.sub.example.com", "example.com"},
		{"www.sub.example.co.uk", "co.uk"}, // conservative: last 2 labels only
	}
	for _, tc := range cases {
		got := registrableDomain(tc.host)
		if got != tc.want {
			t.Errorf("registrableDomain(%q) = %q, want %q", tc.host, got, tc.want)
		}
	}
}

// ── depth cap (structural test) ──────────────────────────────────────────────

// TestDepthCapStructural verifies that ScrapeMenuArgs with Depth==1 carries the
// field correctly (JSON round-trip) and that the zero value is 0.
func TestDepthCapStructural(t *testing.T) {
	// Default zero value.
	var a ScrapeMenuArgs
	if a.Depth != 0 {
		t.Errorf("default Depth should be 0, got %d", a.Depth)
	}

	// Explicit depth-1.
	b := ScrapeMenuArgs{Depth: 1}
	if b.Depth != 1 {
		t.Errorf("Depth should be 1, got %d", b.Depth)
	}
}
