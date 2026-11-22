package link

import "testing"

func TestParse(t *testing.T) {
	cases := map[string]Link{
		"https://pypi.org/project/flask/":                    {Kind: KindPackage, Ecosystem: "pypi", Name: "flask"},
		"https://pypi.org/project/flask/2.0.0/":              {Kind: KindPackage, Ecosystem: "pypi", Name: "flask", Version: "2.0.0"},
		"https://www.npmjs.com/package/express":              {Kind: KindPackage, Ecosystem: "npm", Name: "express"},
		"https://www.npmjs.com/package/@types/node/v/20.1.0": {Kind: KindPackage, Ecosystem: "npm", Name: "@types/node", Version: "20.1.0"},
		"https://crates.io/crates/serde/1.0.200":             {Kind: KindPackage, Ecosystem: "cargo", Name: "serde", Version: "1.0.200"},
		"pkg:npm/@scope/pkg@1.2.3":                           {Kind: KindPackage, Ecosystem: "npm", Name: "@scope/pkg", Version: "1.2.3"},
		"pkg:pypi/requests":                                  {Kind: KindPackage, Ecosystem: "pypi", Name: "requests"},
		"https://github.com/pallets/flask/tree/main":         {Kind: KindRepo, RepoURL: "https://github.com/pallets/flask.git", Name: "flask", Display: "pallets/flask", Ref: "main"},
		"git@github.com:expressjs/express.git":               {Kind: KindRepo, RepoURL: "https://github.com/expressjs/express.git", Name: "express", Display: "expressjs/express"},
	}
	for in, want := range cases {
		got, err := Parse(in)
		if err != nil || *got != want {
			t.Errorf("Parse(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "flask", "https://example.com", "pkg:maven/a/b", "https://pypi.org/", "http://github.com/a/b", "https://pypi.org/user/someone", "https://www.npmjs.com/search/q"} {
		if _, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) must fail", in)
		}
	}
	if !Looks("https://x") || !Looks("pkg:npm/a") || Looks("flask") {
		t.Error("Looks is wrong")
	}
}
