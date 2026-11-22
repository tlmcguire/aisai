// Package websearch lets the chat model look things up on the web: a search
// and a page fetch, both small, bounded and unable to reach the local network.
//
// The search goes to DuckDuckGo's HTML endpoint, which needs no key. What the
// model searches for leaves the machine, so the chat asks the user before the
// first search of a turn.
package websearch

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	searchEndpoint = "https://html.duckduckgo.com/html/"
	userAgent      = "aisai-chat/1.0 (+https://github.com/tlmcguire/aisai)"
	maxPageBytes   = 1 << 20
	maxPageText    = 12000
	maxResults     = 6
)

// Result is one search hit.
type Result struct {
	Title   string
	URL     string
	Snippet string
}

// Client talks to the web. The zero value is ready to use.
type Client struct {
	// Endpoint overrides the search endpoint, for tests.
	Endpoint string
	// HTTP overrides the HTTP client, for tests.
	HTTP *http.Client
	// AllowLocal lets Fetch reach loopback and private addresses, for tests.
	AllowLocal bool
}

func (c *Client) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{
		Timeout: 12 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 4 {
				return errors.New("too many redirects")
			}
			return c.checkURL(req.URL)
		},
	}
}

var (
	resultLink    = regexp.MustCompile(`(?s)<a[^>]+class="result__a"[^>]+href="([^"]+)"[^>]*>(.*?)</a>`)
	resultSnippet = regexp.MustCompile(`(?s)<a[^>]+class="result__snippet"[^>]*>(.*?)</a>`)
	tagPattern    = regexp.MustCompile(`(?s)<[^>]*>`)
	skipBlocks    = []*regexp.Regexp{
		regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`),
		regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`),
		regexp.MustCompile(`(?is)<noscript[^>]*>.*?</noscript>`),
		regexp.MustCompile(`(?is)<svg[^>]*>.*?</svg>`),
		regexp.MustCompile(`(?is)<head[^>]*>.*?</head>`),
	}
	spaces     = regexp.MustCompile(`[ \t\r\f\v]+`)
	blankLines = regexp.MustCompile(`\n{3,}`)
)

// Search returns the top results for a query.
func (c *Client) Search(ctx context.Context, query string) ([]Result, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("web_search requires a query")
	}
	endpoint := c.Endpoint
	if endpoint == "" {
		endpoint = searchEndpoint
	}
	form := url.Values{"q": {query}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("the web search could not be reached: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the web search answered HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPageBytes))
	if err != nil {
		return nil, err
	}
	return parseResults(string(body)), nil
}

func parseResults(page string) []Result {
	links := resultLink.FindAllStringSubmatch(page, -1)
	snippets := resultSnippet.FindAllStringSubmatch(page, -1)
	var out []Result
	for i, l := range links {
		target := realURL(html.UnescapeString(l[1]))
		if target == "" {
			continue
		}
		r := Result{Title: clean(l[2]), URL: target}
		if i < len(snippets) {
			r.Snippet = clean(snippets[i][1])
		}
		out = append(out, r)
		if len(out) == maxResults {
			break
		}
	}
	return out
}

// realURL unwraps DuckDuckGo's redirect link.
func realURL(href string) string {
	if strings.HasPrefix(href, "//") {
		href = "https:" + href
	}
	u, err := url.Parse(href)
	if err != nil {
		return ""
	}
	if target := u.Query().Get("uddg"); target != "" {
		href = target
		if u, err = url.Parse(href); err != nil {
			return ""
		}
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	return href
}

func clean(s string) string {
	return strings.TrimSpace(spaces.ReplaceAllString(html.UnescapeString(tagPattern.ReplaceAllString(s, "")), " "))
}

// checkURL refuses anything but a public http(s) address, so a page the model
// is steered to cannot reach the local network.
func (c *Client) checkURL(u *url.URL) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("only http and https pages can be fetched, not %q", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("the address has no host")
	}
	if c.AllowLocal {
		return nil
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("cannot resolve %s: %w", host, err)
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("%s is on the local network and will not be fetched", host)
		}
	}
	return nil
}

// Fetch downloads a page and returns its readable text.
func (c *Client) Fetch(ctx context.Context, rawURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", fmt.Errorf("bad address: %w", err)
	}
	if err := c.checkURL(u); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,text/plain;q=0.9,*/*;q=0.1")
	resp, err := c.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("the page could not be fetched: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the page answered HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPageBytes))
	if err != nil {
		return "", err
	}
	text := string(body)
	if strings.Contains(resp.Header.Get("Content-Type"), "html") {
		for _, re := range skipBlocks {
			text = re.ReplaceAllString(text, " ")
		}
		text = strings.NewReplacer("</p>", "\n", "<br>", "\n", "<br/>", "\n", "</div>", "\n", "</li>", "\n", "</h1>", "\n", "</h2>", "\n", "</h3>", "\n", "</tr>", "\n").Replace(text)
		text = html.UnescapeString(tagPattern.ReplaceAllString(text, ""))
		text = spaces.ReplaceAllString(text, " ")
		lines := strings.Split(text, "\n")
		for i := range lines {
			lines[i] = strings.TrimSpace(lines[i])
		}
		text = blankLines.ReplaceAllString(strings.Join(lines, "\n"), "\n\n")
	}
	text = strings.TrimSpace(text)
	if len(text) > maxPageText {
		text = text[:maxPageText] + "\n… (page truncated)"
	}
	return text, nil
}
