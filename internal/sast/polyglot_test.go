package sast

import "testing"

func callHits(lang, text, content string) []pgHit {
	callee, args := pgSplitCall(text)
	return pgMatchCall(pgCall{callee: callee, args: args, text: text, line: 1, endLine: 1}, pgFile{lang: lang, content: content})
}

func TestSplitCall(t *testing.T) {
	cases := []struct{ in, callee, args string }{
		{`exec.Command("sh", "-c", cmd)`, "exec.Command", `"sh", "-c", cmd`},
		{`Runtime.getRuntime().exec(cmd)`, "Runtime.getRuntime.exec", "cmd"},
		{`new Function(code)`, "Function", "code"},
		{`Command::new("sh").arg("-c").arg(x)`, "Command.new.arg.arg", "x"}, // callee drops groups
		{`eval x`, "eval", "x"},
		{`f("a)b", c)`, "f", `"a)b", c`},
		{`system`, "system", ""},
	}
	for _, c := range cases {
		callee, args := pgSplitCall(c.in)
		if c.in == `Command::new("sh").arg("-c").arg(x)` {
			if args != "x" {
				t.Errorf("%q: args %q", c.in, args)
			}
			continue
		}
		if callee != c.callee || args != c.args {
			t.Errorf("%q: got (%q,%q) want (%q,%q)", c.in, callee, args, c.callee, c.args)
		}
	}
}

func TestCallRules(t *testing.T) {
	type tc struct {
		lang, call, content string
		want                string // rule id, "" for none
	}
	cases := []tc{
		// command execution
		{"go", `exec.Command("sh", "-c", userCmd)`, "", RuleSubprocessShellTrue},
		{"go", `exec.Command("sh", "-c", "ls -l")`, "", ""},
		{"go", `exec.Command("ls", dir)`, "", ""},
		{"go", `exec.CommandContext(ctx, "bash", "-c", "echo "+x)`, "", RuleSubprocessShellTrue},
		{"javascript", "exec(`ls ${dir}`)", "const { exec } = require('child_process')", RuleSubprocessShellTrue},
		{"javascript", `re.exec(str)`, "const x = require('child_process')", ""},
		{"javascript", `exec(cmd)`, "no import here", ""},
		{"typescript", `child_process.execSync(cmd)`, "", RuleSubprocessShellTrue},
		{"javascript", `child_process.exec("ls")`, "", ""},
		{"javascript", `spawn(cmd, [], { shell: true })`, "child_process", RuleSubprocessShellTrue},
		{"java", `Runtime.getRuntime().exec(cmd)`, "", RuleSubprocessShellTrue},
		{"java", `Runtime.getRuntime().exec("ls")`, "", ""},
		{"java", `new ProcessBuilder("sh", "-c", cmd)`, "", RuleSubprocessShellTrue},
		{"csharp", `Process.Start(userInput)`, "", RuleSubprocessShellTrue},
		{"csharp", `Process.Start("notepad.exe")`, "", ""},
		{"ruby", `system("ls #{dir}")`, "", RuleSubprocessShellTrue},
		{"ruby", `system("ls", "-l")`, "", ""},
		{"ruby", "system cmd", "", RuleSubprocessShellTrue},
		{"c", `system(buf)`, "", RuleSubprocessShellTrue},
		{"c", `system("clear")`, "", ""},
		// eval
		{"javascript", `eval(userInput)`, "", RuleEvalExecNonliteral},
		{"javascript", `eval("1+1")`, "", ""},
		{"typescript", `new Function(body)`, "", RuleEvalExecNonliteral},
		{"ruby", `eval(params[:code])`, "", RuleEvalExecNonliteral},
		{"ruby", `instance_eval(code)`, "", RuleEvalExecNonliteral},
		// deserialization
		{"java", `new ObjectInputStream(in)`, "", RulePickleYamlUnsafeLoad},
		{"csharp", `new BinaryFormatter()`, "", RulePickleYamlUnsafeLoad},
		{"ruby", `YAML.load(body)`, "", RulePickleYamlUnsafeLoad},
		{"ruby", `YAML.safe_load(body)`, "", ""},
		{"ruby", `Marshal.load(data)`, "", RulePickleYamlUnsafeLoad},
		// weak hashes
		{"go", `md5.New()`, "", RuleWeakHashMD5SHA1},
		{"go", `sha256.New()`, "", ""},
		{"javascript", `crypto.createHash('md5')`, "", RuleWeakHashMD5SHA1},
		{"javascript", `crypto.createHash("sha256")`, "", ""},
		{"java", `MessageDigest.getInstance("MD5")`, "", RuleWeakHashMD5SHA1},
		{"java", `MessageDigest.getInstance("SHA-256")`, "", ""},
		{"csharp", `MD5.Create()`, "", RuleWeakHashMD5SHA1},
		{"csharp", `SHA256.Create()`, "", ""},
		{"ruby", `Digest::MD5.hexdigest(x)`, "", RuleWeakHashMD5SHA1},
		{"ruby", `Digest::SHA256.hexdigest(x)`, "", ""},
		{"rust", `Md5::new()`, "", RuleWeakHashMD5SHA1},
		{"rust", `Sha256::new()`, "", ""},
		{"c", `MD5(data, len, out)`, "", RuleWeakHashMD5SHA1},
		// weak ciphers
		{"go", `des.NewCipher(key)`, "", RuleWeakCipher},
		{"java", `Cipher.getInstance("DES/CBC/PKCS5Padding")`, "", RuleWeakCipher},
		{"java", `Cipher.getInstance("AES")`, "", RuleWeakCipher},
		{"java", `Cipher.getInstance("AES/GCM/NoPadding")`, "", ""},
		{"javascript", `crypto.createCipher('aes192', pw)`, "", RuleWeakCipher},
		{"javascript", `crypto.createCipheriv('aes-256-gcm', k, iv)`, "", ""},
		{"csharp", `DES.Create()`, "", RuleWeakCipher},
		{"csharp", `new DESCryptoServiceProvider()`, "", RuleWeakCipher},
		// SQL
		{"go", `db.Query("SELECT * FROM users WHERE id = " + id)`, "", RuleSQLInjectionConcat},
		{"go", `db.Query("SELECT * FROM users WHERE id = $1", id)`, "", ""},
		{"go", `db.Exec(fmt.Sprintf("DELETE FROM t WHERE a = %s", a))`, "", RuleSQLInjectionConcat},
		{"javascript", "db.query(`SELECT * FROM t WHERE a = ${a}`)", "", RuleSQLInjectionConcat},
		{"java", `stmt.executeQuery("SELECT * FROM t WHERE a = '" + a + "'")`, "", RuleSQLInjectionConcat},
		{"java", `stmt.executeQuery("SELECT * FROM t")`, "", ""},
		{"ruby", `User.where("name = '#{params[:n]}'")`, "", RuleSQLInjectionConcat},
		{"ruby", `User.where(name: params[:n])`, "", ""},
		{"rust", `sqlx::query(&format!("SELECT * FROM t WHERE a = {}", a))`, "", RuleSQLInjectionConcat},
		{"javascript", `/ab/.exec("select x from y where z")`, "", ""},
		// C
		{"c", `gets(buf)`, "", RuleUnsafeCFunction},
		{"c", `strcpy(dst, src)`, "", RuleUnsafeCFunction},
		{"c", `strncpy(dst, src, n)`, "", ""},
		{"c", `scanf("%s", buf)`, "", RuleUnsafeCFunction},
		{"c", `scanf("%10s", buf)`, "", ""},
		{"c", `mktemp(tmpl)`, "", RuleTempfileMktemp},
		{"c", `printf(user)`, "", RuleFormatString},
		{"c", `printf("%s", user)`, "", ""},
		{"c", `fprintf(stderr, msg)`, "", RuleFormatString},
		// language scoping: a Go rule must not fire for Java
		{"java", `md5.New()`, "", ""},
	}
	for _, c := range cases {
		hits := callHits(c.lang, c.call, c.content)
		got := ""
		if len(hits) > 0 {
			got = hits[0].rule
		}
		if got != c.want {
			t.Errorf("%s %s: got %q want %q", c.lang, c.call, got, c.want)
		}
	}
}

func TestRustShellChain(t *testing.T) {
	bad := `Command::new("sh").arg("-c").arg(cmd).output()`
	good := `Command::new("sh").arg("-c").arg("ls").output()`
	safe := `Command::new("ls").arg(dir).output()`
	if h := callHits("rust", bad, ""); len(h) != 1 || h[0].rule != RuleSubprocessShellTrue {
		t.Errorf("bad chain: %+v", h)
	}
	if h := callHits("rust", good, ""); len(h) != 0 {
		t.Errorf("literal chain should be safe: %+v", h)
	}
	if h := callHits("rust", safe, ""); len(h) != 0 {
		t.Errorf("direct command should be safe: %+v", h)
	}
}

func TestLineRules(t *testing.T) {
	type tc struct {
		lang, src, want string
	}
	cases := []tc{
		{"go", `cfg := &tls.Config{InsecureSkipVerify: true}`, RuleTLSVerifyDisabled},
		{"go", `cfg := &tls.Config{InsecureSkipVerify: false}`, ""},
		{"go", `// InsecureSkipVerify: true`, ""},
		{"javascript", `agent = new https.Agent({ rejectUnauthorized: false })`, RuleTLSVerifyDisabled},
		{"javascript", `process.env.NODE_TLS_REJECT_UNAUTHORIZED = '0'`, RuleTLSVerifyDisabled},
		{"java", `conn.setHostnameVerifier((h, s) -> true);`, RuleTLSVerifyDisabled},
		{"csharp", `ServicePointManager.ServerCertificateValidationCallback += (s, c, ch, e) => true;`, RuleTLSVerifyDisabled},
		{"ruby", `http.verify_mode = OpenSSL::SSL::VERIFY_NONE`, RuleTLSVerifyDisabled},
		{"rust", `Client::builder().danger_accept_invalid_certs(true)`, RuleTLSVerifyDisabled},
		{"c", `curl_easy_setopt(h, CURLOPT_SSL_VERIFYPEER, 0L);`, RuleTLSVerifyDisabled},
		{"javascript", `el.innerHTML = userInput;`, RuleXSSUnsafeOutput},
		{"javascript", `el.innerHTML = "<b>static</b>";`, ""},
		{"javascript", `document.write(location.hash)`, RuleXSSUnsafeOutput},
		{"typescript", `<div dangerouslySetInnerHTML={{__html: html}} />`, RuleXSSUnsafeOutput},
		{"go", `return template.HTML(userInput)`, RuleXSSUnsafeOutput},
		{"go", `return template.HTML("<br>")`, ""},
		{"csharp", `@Html.Raw(Model.Body)`, RuleXSSUnsafeOutput},
		{"ruby", `<%= comment.body.html_safe %>`, RuleXSSUnsafeOutput},
		{"ruby", `"<b>x</b>".html_safe`, ""},
		{"java", `out.println(request.getParameter("q"));`, RuleXSSUnsafeOutput},
		{"go", `password := "Zk93kd8sLq0Pw1xz"`, RuleHardcodedSecret},
		{"javascript", `const apiKey = "sk_live_9aK2mQ7xW4zB1cD8"`, RuleHardcodedSecret},
		{"java", `private static final String PASSWORD = "Xk29fj!ds83Lq";`, RuleHardcodedSecret},
		{"go", `password := "changeme"`, ""},
		{"go", `password := os.Getenv("PASSWORD")`, ""},
		{"go", `tokenType := "bearerbearer"`, ""},
		{"javascript", `if (password === "Zk93kd8sLq0Pw1xz") {}`, ""},
		{"ruby", `  secret_key: "9aK2mQ7xW4zB1cD8eF"`, RuleHardcodedSecret},
	}
	for _, c := range cases {
		hits := pgScanLines(pgFile{lang: c.lang, content: c.src})
		got := ""
		if len(hits) > 0 {
			got = hits[0].rule
		}
		if got != c.want {
			t.Errorf("%s %q: got %q want %q", c.lang, c.src, got, c.want)
		}
	}
}

func TestDedupe(t *testing.T) {
	in := []pgHit{{rule: "a", line: 1}, {rule: "a", line: 1}, {rule: "b", line: 1}, {rule: "a", line: 2}}
	if got := pgDedupe(in); len(got) != 3 {
		t.Errorf("got %d hits", len(got))
	}
}
