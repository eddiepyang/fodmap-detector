package menusearch

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"syscall"
	"testing"
	"time"
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

func TestIsNonMenuURL(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		// Blocklist — must be hard-dropped.
		{"https://vertexaisearch.cloud.google.com/grounding-api-redirect/abc123", true},
		{"https://www.loopnet.com/listing/123-main-st", true},
		{"https://sub.loopnet.com/foo", true},
		{"https://hilton.com/en/hotels/nyc/", true},
		{"https://www.checkle.com/business/my-place", true},
		{"https://streeteasy.com/building/123-main", true},
		{"https://realtor.com/realestateandhomes-detail/foo", true},
		{"https://zillow.com/homes/for_sale/", true},
		{"https://crexi.com/properties/123", true},
		{"https://marriott.com/hotels/travel/nyc/", true},
		{"https://booking.com/hotel/us/foo.html", true},
		{"https://expedia.com/New-York-Hotels.html", true},
		{"https://hotels.com/ho123456/foo/", true},
		{"https://mapquest.com/search/results?query=foo", true},
		{"https://yellowpages.com/search?q=foo", true},
		{"https://bbb.org/us/ny/new-york/profile/foo", true},
		// Not blocklisted — must pass through.
		{"https://myrestaurant.com/menu", false},
		{"https://toasttab.com/my-restaurant/v3", false},
		{"https://getsauce.com/order/my-restaurant", false},
		{"https://order.online/store/my-place", false},
		{"https://doordash.com/store/my-restaurant", false}, // delivery, not non-menu
	}
	for _, tc := range cases {
		if got := isNonMenuURL(tc.url); got != tc.want {
			t.Errorf("isNonMenuURL(%q) = %v, want %v", tc.url, got, tc.want)
		}
	}
}

func TestIsDeadDomainErr(t *testing.T) {
	// DNS NXDOMAIN → dead domain.
	dnsNotFound := &net.DNSError{Err: "no such host", Name: "doesnotexist.example", IsNotFound: true}
	if !isDeadDomainErr(dnsNotFound) {
		t.Error("expected isDeadDomainErr=true for DNS NXDOMAIN")
	}

	// DNS timeout (not NXDOMAIN) → NOT a dead-domain signal; anti-bot may block DNS.
	dnsTimeout := &net.DNSError{Err: "i/o timeout", Name: "example.com", IsTimeout: true}
	if isDeadDomainErr(dnsTimeout) {
		t.Error("expected isDeadDomainErr=false for DNS timeout")
	}

	// ECONNREFUSED → dead domain (port closed).
	if !isDeadDomainErr(syscall.ECONNREFUSED) {
		t.Error("expected isDeadDomainErr=true for ECONNREFUSED")
	}

	// TLS certificate errors → dead domain.
	certInvalid := tls.AlertError(42) // arbitrary TLS alert
	if !isDeadDomainErr(certInvalid) {
		t.Error("expected isDeadDomainErr=true for tls.AlertError")
	}

	// Generic error → NOT a dead-domain signal.
	if isDeadDomainErr(net.ErrClosed) {
		t.Error("expected isDeadDomainErr=false for net.ErrClosed")
	}

	// nil → not dead.
	if isDeadDomainErr(nil) {
		t.Error("expected isDeadDomainErr=false for nil")
	}
}

// mockTransport intercepts requests to fakeHost and routes them to a real
// backend address, letting us test reachableMenuURLs with non-private hostnames.
type mockTransport struct {
	backendAddr string // e.g. "127.0.0.1:PORT"
	inner       http.RoundTripper
}

func (m *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone the request and rewrite the host to the backend.
	cloned := req.Clone(req.Context())
	cloned.URL.Host = m.backendAddr
	cloned.Host = m.backendAddr
	return m.inner.RoundTrip(cloned)
}

func TestReachableMenuURLs_LiveServerKept(t *testing.T) {
	// An httptest server returning 403 (anti-bot block) must be KEPT.
	// We bind the server on loopback but route requests via a non-private
	// hostname ("example-restaurant.test") so isPrivateMenuHost doesn't
	// short-circuit the probe before we even dial.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	// Strip scheme+host from srv.URL to get the backend addr.
	backendAddr := srv.Listener.Addr().String()
	client := &http.Client{
		Transport: &mockTransport{
			backendAddr: backendAddr,
			inner:       http.DefaultTransport,
		},
		Timeout: 5 * time.Second,
	}

	fakeURL := "http://example-restaurant.test/menu"
	got := reachableMenuURLs(context.Background(), client, []string{fakeURL})
	if len(got) != 1 || got[0] != fakeURL {
		t.Errorf("expected 403 server to be kept; got %v", got)
	}
}

func TestReachableMenuURLs_DeadHostDropped(t *testing.T) {
	// Use a non-private hostname routed via mockTransport to a port that is
	// certainly closed (port 1 on loopback), producing ECONNREFUSED → drop.
	client := &http.Client{
		Transport: &mockTransport{
			backendAddr: "127.0.0.1:1",
			inner:       http.DefaultTransport,
		},
		Timeout: 3 * time.Second,
	}
	fakeURL := "http://dead-restaurant.test/menu"
	got := reachableMenuURLs(context.Background(), client, []string{fakeURL})
	if len(got) != 0 {
		t.Errorf("expected dead-port URL to be dropped; got %v", got)
	}
}

// ── hasMenuSignal tests ──────────────────────────────────────────────────────

func TestHasMenuSignal_JSONLD(t *testing.T) {
	// JSON-LD with a Menu type → true.
	body := []byte(`<html><head>
<script type="application/ld+json">
{"@context":"https://schema.org","@type":"Menu","name":"Dinner Menu",
 "hasMenuSection":[{"@type":"MenuSection","name":"Mains",
   "hasMenuItem":[{"@type":"MenuItem","name":"Burger","offers":{"price":"12.00"}}]}]}
</script></head><body></body></html>`)
	if !hasMenuSignal(body) {
		t.Error("expected hasMenuSignal=true for JSON-LD Menu document")
	}
}

func TestHasMenuSignal_PricesAndKeyword(t *testing.T) {
	// Several prices + menu keyword → true.
	body := []byte(`<html><body>
<h1>Our Menu</h1>
<p>Salad $8.00 &nbsp; Soup $6.50 &nbsp; Burger $13.00</p>
</body></html>`)
	if !hasMenuSignal(body) {
		t.Error("expected hasMenuSignal=true for page with prices and 'menu' keyword")
	}
}

func TestHasMenuSignal_RealEstatePage(t *testing.T) {
	// Generic real-estate page with no menu signals → false.
	body := []byte(`<html><body>
<h1>Prime Commercial Space for Lease</h1>
<p>1,200 sq ft available in Midtown. Contact our broker for details.
Floor plan available upon request. Zoned for retail or office use.</p>
</body></html>`)
	if hasMenuSignal(body) {
		t.Error("expected hasMenuSignal=false for real-estate page")
	}
}

func TestHasMenuSignal_SchemaOrgType(t *testing.T) {
	// Inline schema.org MenuItem attribute → true.
	body := []byte(`<html><body>
<div itemtype="http://schema.org/MenuItem">
  <span itemprop="name">Caesar Salad</span>
</div>
</body></html>`)
	if !hasMenuSignal(body) {
		t.Error("expected hasMenuSignal=true for schema.org MenuItem")
	}
}

// ── menuSignalFilter / checkMenuSignal policy tests ─────────────────────────

func TestCheckMenuSignal_403Kept(t *testing.T) {
	// A server returning 403 must always be kept (anti-bot).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	client := &http.Client{
		Transport: &mockTransport{backendAddr: srv.Listener.Addr().String(), inner: http.DefaultTransport},
		Timeout:   5 * time.Second,
	}
	keep, reason := checkMenuSignal(context.Background(), client, "http://restaurant.test/menu", "")
	if !keep {
		t.Errorf("expected 403 response to be kept, got keep=false reason=%q", reason)
	}
}

func TestCheckMenuSignal_OrderingPlatformKept(t *testing.T) {
	// A whitelisted ordering platform must be kept regardless of body content.
	// We don't even need a real server — the whitelist check fires before the GET.
	client := &http.Client{Timeout: 1 * time.Second}
	keep, reason := checkMenuSignal(context.Background(), client, "https://order.toasttab.com/online/my-place", "")
	if !keep {
		t.Errorf("expected ordering platform to be kept, got keep=false reason=%q", reason)
	}
}

func TestCheckMenuSignal_2xxWithSignalKept(t *testing.T) {
	// 2xx response with menu JSON-LD → kept.
	body := []byte(`<html><head>
<script type="application/ld+json">
{"@context":"https://schema.org","@type":"MenuItem","name":"Pasta","offers":{"price":"14.00"}}
</script></head><body>Our menu</body></html>`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	client := &http.Client{
		Transport: &mockTransport{backendAddr: srv.Listener.Addr().String(), inner: http.DefaultTransport},
		Timeout:   5 * time.Second,
	}
	keep, _ := checkMenuSignal(context.Background(), client, "http://restaurant.test/menu", "")
	if !keep {
		t.Error("expected 2xx page with menu signal to be kept")
	}
}

func TestCheckMenuSignal_2xxNoSignalDropped(t *testing.T) {
	// 2xx response with generic non-menu content → dropped.
	body := []byte(`<html><body><h1>About Us</h1><p>We are a family restaurant founded in 1990.</p></body></html>`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	client := &http.Client{
		Transport: &mockTransport{backendAddr: srv.Listener.Addr().String(), inner: http.DefaultTransport},
		Timeout:   5 * time.Second,
	}
	keep, _ := checkMenuSignal(context.Background(), client, "http://restaurant.test/about", "")
	if keep {
		t.Error("expected 2xx page with no menu signal to be dropped")
	}
}

func TestCheckMenuSignal_PrimaryURLAlwaysKept(t *testing.T) {
	// The primary URL must be kept regardless of page content — the check fires
	// before any network call, so a non-dialing client is safe here.
	client := &http.Client{Timeout: time.Second}
	keep, reason := checkMenuSignal(context.Background(), client,
		"https://restaurant.example.com", "https://restaurant.example.com")
	if !keep {
		t.Errorf("primary URL must always be kept, got keep=false reason=%q", reason)
	}
	if reason != "primary website URL (always keep)" {
		t.Errorf("unexpected reason %q, want %q", reason, "primary website URL (always keep)")
	}
}

func TestCheckMenuSignal_PrimaryURLTrailingSlashNormalised(t *testing.T) {
	// A trailing slash on either side must not prevent the primary URL match.
	client := &http.Client{Timeout: time.Second}
	keep, reason := checkMenuSignal(context.Background(), client,
		"https://restaurant.example.com/", "https://restaurant.example.com")
	if !keep {
		t.Errorf("primary URL with trailing slash must be kept, got keep=false reason=%q", reason)
	}
	if reason != "primary website URL (always keep)" {
		t.Errorf("unexpected reason %q", reason)
	}
}

func TestCheckMenuSignal_EmptyPrimaryURLSkipsPin(t *testing.T) {
	// When primaryURL is empty, the pin check must not fire — a no-signal 2xx
	// page must still be dropped normally.
	body := []byte(`<html><body><p>Generic page with no menu content.</p></body></html>`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	client := &http.Client{
		Transport: &mockTransport{backendAddr: srv.Listener.Addr().String(), inner: http.DefaultTransport},
		Timeout:   5 * time.Second,
	}
	keep, _ := checkMenuSignal(context.Background(), client, "http://restaurant.test/about", "")
	if keep {
		t.Error("expected page to be dropped when primaryURL is empty and there is no menu signal")
	}
}
