package registry

import (
	"context"
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{"": PyPI, "PyPI": PyPI, "NPM": NPM, "crates.io": Crate, "rust": Crate} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSetGetUnknown(t *testing.T) {
	if _, err := NewSet().Get("maven"); err == nil {
		t.Fatal("expected an error for an unknown ecosystem")
	}
}

func TestNPMLookupAndDownload(t *testing.T) {
	body := []byte("not really a tarball")
	sum := sha512.Sum512(body)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.EscapedPath() {
		case "/@scope%2fpkg":
			fmt.Fprintf(w, `{"name":"@scope/pkg","dist-tags":{"latest":"1.2.0"},"versions":{
			 "1.0.0":{"version":"1.0.0"},
			 "1.2.0":{"version":"1.2.0","description":"hi","dist":{"tarball":"%s/t.tgz","integrity":"sha512-%s"}}}}`,
				srv.URL, base64.StdEncoding.EncodeToString(sum[:]))
		case "/t.tgz":
			w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	reg := NewNPM(NPMOptions{BaseURL: srv.URL})
	info, err := reg.Lookup(context.Background(), "@scope/pkg")
	if err != nil {
		t.Fatal(err)
	}
	if info.Latest != "1.2.0" || info.Summary != "hi" || len(info.Versions) != 2 {
		t.Fatalf("unexpected info: %+v", info)
	}
	path, ver, err := reg.Download(context.Background(), "@scope/pkg", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if ver != "1.2.0" {
		t.Errorf("resolved version = %q", ver)
	}
	if got, _ := os.ReadFile(path); string(got) != string(body) {
		t.Errorf("downloaded content differs")
	}
	if _, err := reg.Lookup(context.Background(), "missing"); err == nil {
		t.Error("expected not found")
	}
}

func TestNPMIntegrityMismatch(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/p" {
			fmt.Fprintf(w, `{"name":"p","dist-tags":{"latest":"1.0.0"},"versions":{"1.0.0":{"dist":{"tarball":"%s/t.tgz","shasum":"deadbeef"}}}}`, srv.URL)
			return
		}
		w.Write([]byte("tampered"))
	}))
	defer srv.Close()
	if _, _, err := NewNPM(NPMOptions{BaseURL: srv.URL}).Download(context.Background(), "p", "", t.TempDir()); err == nil {
		t.Fatal("expected an integrity failure")
	}
}

func TestCratesLookupAndDownload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/crates/serde":
			fmt.Fprint(w, `{"crate":{"name":"serde","description":"d","max_stable_version":"1.0.1"},"versions":[{"num":"1.0.1"},{"num":"1.0.0","yanked":true}]}`)
		case "/api/v1/crates/serde/1.0.1/download":
			w.Write([]byte("crate"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	reg := NewCrates(CratesOptions{BaseURL: srv.URL})
	info, err := reg.Lookup(context.Background(), "serde")
	if err != nil || info.Latest != "1.0.1" || len(info.Versions) != 1 {
		t.Fatalf("info=%+v err=%v", info, err)
	}
	if _, ver, err := reg.Download(context.Background(), "serde", "", t.TempDir()); err != nil || ver != "1.0.1" {
		t.Fatalf("ver=%q err=%v", ver, err)
	}
}
