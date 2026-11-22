package websearch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseResultsUnwrapsRedirects(t *testing.T) {
	page := `<a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fa%3Fx%3D1&rut=z">Flask <b>docs</b></a>
<a class="result__snippet" href="x">The &amp; snippet</a>
<a class="result__a" href="javascript:alert(1)">bad</a>`
	got := parseResults(page)
	if len(got) != 1 || got[0].URL != "https://example.com/a?x=1" || got[0].Title != "Flask docs" || got[0].Snippet != "The & snippet" {
		t.Fatalf("results: %+v", got)
	}
}

func TestFetchRefusesLocalAddressesAndStripsHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><head><title>t</title></head><body><script>evil()</script><p>Hello <b>world</b></p></body></html>"))
	}))
	defer srv.Close()

	if _, err := (&Client{}).Fetch(context.Background(), srv.URL); err == nil || !strings.Contains(err.Error(), "local network") {
		t.Fatalf("a loopback address must be refused, got %v", err)
	}
	if _, err := (&Client{}).Fetch(context.Background(), "file:///etc/passwd"); err == nil {
		t.Fatal("non-http schemes must be refused")
	}
	text, err := (&Client{AllowLocal: true}).Fetch(context.Background(), srv.URL)
	if err != nil || strings.Contains(text, "evil") || !strings.Contains(text, "Hello world") {
		t.Fatalf("text %q err %v", text, err)
	}
}
