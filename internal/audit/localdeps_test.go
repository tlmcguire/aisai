package audit

import "testing"

func TestLocalManifestParsers(t *testing.T) {
	req := parseRequirements("# c\nflask>=2.0\nrequests[security]==2.31 ; python_version>'3'\n-r other.txt\nclick\n")
	if len(req) != 3 || req[0].Name != "flask" || req[0].Version != ">=2.0" || req[1].Name != "requests" || req[2].Version != "" {
		t.Fatalf("requirements: %+v", req)
	}
	py := parsePyproject("[project]\nname=\"x\"\ndependencies = [\n  \"httpx>=0.27\",\n  'rich',\n]\n[tool.x]\ndependencies=[\"no\"]\n")
	if len(py) != 2 || py[0].Name != "httpx" || py[1].Name != "rich" {
		t.Fatalf("pyproject: %+v", py)
	}
	js := parsePackageJSON(`{"dependencies":{"a":"^1"},"devDependencies":{"b":"2"}}`)
	if len(js) != 2 || js[0].Name != "a" {
		t.Fatalf("package.json: %+v", js)
	}
	gm := parseGoMod("module x\nrequire (\n\tgithub.com/a/b v1.2.3 // indirect\n)\nrequire golang.org/x/y v0.1.0\n")
	if len(gm) != 2 || gm[1].Version != "v0.1.0" {
		t.Fatalf("go.mod: %+v", gm)
	}
	cg := parseCargo("[package]\nname=\"x\"\n[dependencies]\nserde = \"1\"\ntokio = { version = \"1.2\", features = [\"full\"] }\n")
	if len(cg) != 2 || cg[1].Version != "1.2" {
		t.Fatalf("cargo: %+v", cg)
	}
}
