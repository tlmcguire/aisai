package sast

import (
	"strings"
	"testing"

	"github.com/tlmcguire/aisai/internal/ast"
	"github.com/tlmcguire/aisai/internal/model"
)

// taintRules parses a source file and returns the rule ids the taint
// analysis reports, with their lines.
func taintFindings(t *testing.T, path, src string) []model.Finding {
	t.Helper()
	f, err := ast.ParseString(path, src)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return scanTaint(f)
}

func hasRule(fs []model.Finding, rule string) bool {
	for _, f := range fs {
		if f.Rule == rule {
			return true
		}
	}
	return false
}

func TestTaintFindsFlowsAndIgnoresSafeCode(t *testing.T) {
	cases := []struct {
		name string
		path string
		src  string
		want string // rule expected, or "" for none
	}{
		// ---- Rust
		{"rust env to shell", "a.rs", `
use std::process::Command;
fn run() {
    let name = std::env::args().nth(1).unwrap();
    let cmd = format!("ls {}", name);
    Command::new("sh").arg("-c").arg(&cmd).output().unwrap();
}`, RuleTaintCommand},
		{"rust inline format arg", "a.rs", `
fn run() {
    let name = std::env::var("X").unwrap();
    let cmd = format!("ls {name}");
    Command::new("sh").arg("-c").arg(cmd).status().unwrap();
}`, RuleTaintCommand},
		{"rust literal command is fine", "a.rs", `
fn run() {
    Command::new("ls").arg("-l").output().unwrap();
}`, ""},
		{"rust path from request", "a.rs", `
fn handler(req: &Request) -> String {
    let p = req.param("file");
    std::fs::read_to_string(p).unwrap()
}`, RuleTaintPath},
		{"rust basename sanitizes the path", "a.rs", `
fn handler(req: &Request) -> String {
    let p = req.param("file");
    let safe = Path::new(&p).file_name().unwrap();
    std::fs::read_to_string(safe).unwrap()
}`, ""},
		{"rust parsed length reaches from_raw_parts", "a.rs", `
fn read(buf: &[u8]) {
    let mut line = String::new();
    std::io::stdin().read_line(&mut line).unwrap();
    let n: usize = line.trim().parse().unwrap();
    unsafe { std::slice::from_raw_parts(buf.as_ptr(), n) };
}`, RuleTaintMemory},
		{"rust parsed number is not a path", "a.rs", `
fn read() {
    let mut line = String::new();
    std::io::stdin().read_line(&mut line).unwrap();
    let n: u32 = line.parse().unwrap();
    std::fs::read_to_string(n).unwrap();
}`, ""},
		{"rust allocation from file", "a.rs", `
fn parse() {
    let data = std::fs::read("model.bin").unwrap();
    let count = data.len();
    let n = u32::from_le_bytes([data[0], data[1], data[2], data[3]]);
    let v: Vec<u8> = Vec::with_capacity(n as usize);
}`, RuleTaintAlloc},
		{"rust overwritten before use", "a.rs", `
fn run() {
    let mut p = std::env::args().nth(1).unwrap();
    p = String::from("fixed.txt");
    std::fs::read_to_string(p).unwrap();
}`, ""},
		{"rust through a helper in the same file", "a.rs", `
fn open(path: &str) -> String { std::fs::read_to_string(path).unwrap() }
fn main() {
    let p = std::env::args().nth(1).unwrap();
    open(&p);
}`, RuleTaintPath},
		// ---- JavaScript
		{"js query to exec", "a.js", `
function h(req, res) {
  const name = req.query.name;
  const cmd = "ls " + name;
  child_process.exec(cmd);
}`, RuleTaintCommand},
		{"js sanitized by encodeURIComponent", "a.js", `
function h(req, res) {
  const name = encodeURIComponent(req.query.name);
  child_process.exec("ls " + name);
}`, ""},
		{"js innerHTML", "a.js", `
function h() {
  const q = location.search;
  document.body.innerHTML = q;
}`, RuleTaintXSS},
		{"js parameterized query is fine", "a.js", `
function h(req, res) {
  db.query("select * from t where id = ?", [req.query.id]);
}`, ""},
		{"js concatenated query", "a.js", `
function h(req, res) {
  db.query("select * from t where id = " + req.query.id);
}`, RuleTaintSQL},
		{"ts eval through arrow", "a.ts", `
const run = (x: string) => eval(x);
function h(req: any, res: any) { run(req.body.code); }`, RuleTaintCode},
		// ---- Go
		{"go query to exec", "a.go", `package main
func h(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("n")
	q := "x" + name
	exec.Command("sh", "-c", q).Run()
}`, RuleTaintCommand},
		{"go strconv makes it a number, not a command", "a.go", `package main
func h(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(r.FormValue("n"))
	exec.Command("sleep", strconv.Itoa(n)).Run()
}`, ""},
		{"go sql concat", "a.go", `package main
func h(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("id")
	db.Query("select * from t where id = " + id)
}`, RuleTaintSQL},
		{"go sql with parameter is fine", "a.go", `package main
func h(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("id")
	db.Query("select * from t where id = ?", id)
}`, ""},
		{"go path from args", "a.go", `package main
func main() {
	p := os.Args[1]
	os.ReadFile(p)
}`, RuleTaintPath},
		// ---- Java
		{"java request to exec", "A.java", `class A {
  void h(HttpServletRequest request) {
    String name = request.getParameter("n");
    String q = "x" + name;
    Runtime.getRuntime().exec(q);
  }
}`, RuleTaintCommand},
		{"java reassigned clean", "A.java", `class A {
  void h(HttpServletRequest request) {
    String q = request.getParameter("n");
    q = "fixed";
    Runtime.getRuntime().exec(q);
  }
}`, ""},
		{"java sql", "A.java", `class A {
  void h(HttpServletRequest request) {
    String id = request.getParameter("id");
    stmt.executeQuery("select * from t where id = " + id);
  }
}`, RuleTaintSQL},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := taintFindings(t, c.path, c.src)
			if c.want == "" {
				if len(got) != 0 {
					t.Fatalf("expected no finding, got %s", describe(got))
				}
				return
			}
			if !hasRule(got, c.want) {
				t.Fatalf("expected %s, got %s", c.want, describe(got))
			}
		})
	}
}

func describe(fs []model.Finding) string {
	if len(fs) == 0 {
		return "nothing"
	}
	var parts []string
	for _, f := range fs {
		parts = append(parts, f.Rule+"@"+itoa(f.StartLine))
	}
	return strings.Join(parts, ", ")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// A finding explains itself: where the value came from and how it got there.
func TestTaintFindingCarriesItsPath(t *testing.T) {
	got := taintFindings(t, "a.go", `package main
func h(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("n")
	q := "x" + name
	exec.Command("sh", "-c", q).Run()
}`)
	if len(got) == 0 {
		t.Fatal("no finding")
	}
	f := got[0]
	if f.Evidence.Source == "" || f.Evidence.Sink == "" || len(f.Evidence.Flow) < 3 {
		t.Fatalf("evidence is thin: %+v", f.Evidence)
	}
	flow := strings.Join(f.Evidence.Flow, "\n")
	for _, want := range []string{"name =", "q =", "exec"} {
		if !strings.Contains(flow, want) {
			t.Errorf("the flow does not mention %q:\n%s", want, flow)
		}
	}
}

// A check on the value lowers the confidence instead of hiding the finding.
func TestTaintGuardLowersConfidence(t *testing.T) {
	plain := taintFindings(t, "a.go", `package main
func h(w http.ResponseWriter, r *http.Request) {
	p := r.FormValue("p")
	os.ReadFile(p)
}`)
	guarded := taintFindings(t, "a.go", `package main
func h(w http.ResponseWriter, r *http.Request) {
	p := r.FormValue("p")
	if !strings.HasPrefix(p, "/safe/") { return }
	os.ReadFile(p)
}`)
	if len(plain) == 0 || len(guarded) == 0 {
		t.Fatalf("both should report: plain=%s guarded=%s", describe(plain), describe(guarded))
	}
	if guarded[0].Confidence >= plain[0].Confidence {
		t.Errorf("a guarded value should be less confident: %.2f vs %.2f", guarded[0].Confidence, plain[0].Confidence)
	}
}

// A safe public function that hands its argument to raw memory breaks its promise.
func TestRustPublicFunctionParameterReachingRawMemory(t *testing.T) {
	got := taintFindings(t, "lib.rs", `
pub fn view(ptr: *const u8, len: usize) -> &'static [u8] {
    unsafe { std::slice::from_raw_parts(ptr, len) }
}
pub unsafe fn view_unsafe(ptr: *const u8, len: usize) -> &'static [u8] {
    std::slice::from_raw_parts(ptr, len)
}`)
	var public, unsafeFn int
	for _, f := range got {
		if f.Rule != RuleTaintMemory {
			continue
		}
		if strings.Contains(f.Message, "`view`") {
			public++
		}
		if strings.Contains(f.Message, "view_unsafe") {
			unsafeFn++
		}
	}
	if public != 1 {
		t.Errorf("the safe public function should be reported once, got %d (%s)", public, describe(got))
	}
	if unsafeFn != 0 {
		t.Errorf("an unsafe fn states its contract; it should not be reported, got %d", unsafeFn)
	}
}

func TestRustUnsafeRules(t *testing.T) {
	f, err := ast.ParseString("a.rs", `
static mut COUNTER: u32 = 0;
unsafe impl Send for Foo {}
extern "C" { fn c_func(x: i32) -> i32; }
#[no_mangle]
pub extern "C" fn exported(p: *const u8) {}
pub unsafe fn danger(len: usize) -> &'static [u8] {
    std::slice::from_raw_parts(PTR, len)
}
fn wrapper() {
    unsafe {
        let x: u32 = std::mem::transmute(1.0f32);
        c_func(3);
    }
}
fn clean() { let v = vec![1, 2, 3]; }`)
	if err != nil {
		t.Fatal(err)
	}
	got := scanRustUnsafe(f)
	rules := map[string]int{}
	for _, g := range got {
		rules[g.Rule]++
	}
	for _, want := range []string{RuleRustStaticMut, RuleRustSendSync, RuleRustFFI, RuleRustUnsafe, RuleRustUnsafeOp} {
		if rules[want] == 0 {
			t.Errorf("missing %s in %v", want, rules)
		}
	}
	// transmute and from_raw_parts are reported one by one; the call to the
	// foreign c_func is named in its unsafe block's finding instead.
	if rules[RuleRustUnsafeOp] != 2 {
		t.Errorf("want 2 unchecked operations, got %d", rules[RuleRustUnsafeOp])
	}
	for _, g := range got {
		if strings.Contains(g.Message, "clean") {
			t.Errorf("safe code was reported: %s", g.Message)
		}
	}
}
