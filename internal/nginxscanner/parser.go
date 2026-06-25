package nginxscanner

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var (
	reServerName = regexp.MustCompile(`server_name\s+([^;]+);`)
	reListen     = regexp.MustCompile(`listen\s+([^;]+);`)
	reSSLCert    = regexp.MustCompile(`ssl_certificate\s+([^;]+);`)
	reProxyPass  = regexp.MustCompile(`proxy_pass\s+([^;]+);`)
)

// ParsedBlock holds parsed directives from one nginx server{} block.
type ParsedBlock struct {
	ServerNames []string
	ListenPorts []int
	SSLEnabled  bool
	CertPath    string
	ProxyPass   string
}

func removeComments(content string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if idx := strings.IndexByte(line, '#'); idx >= 0 {
			lines[i] = line[:idx]
		}
	}
	return strings.Join(lines, "\n")
}

// extractServerBlocks finds all top-level server{} block contents by
// tracking brace depth. Only blocks whose keyword is exactly "server" are
// captured; upstream{}, http{}, etc. are skipped.
func extractServerBlocks(content string) []string {
	var blocks []string
	i := 0
	n := len(content)

	for i < n {
		for i < n && isWS(content[i]) {
			i++
		}
		if i >= n {
			break
		}

		ch := content[i]
		switch {
		case ch == ';' || ch == '}':
			i++
		case ch == '{':
			i = skipBlock(content, i)
		default:
			// Read the next token (keyword or value)
			j := i
			for j < n && !isWS(content[j]) && content[j] != '{' && content[j] != '}' && content[j] != ';' {
				j++
			}
			word := content[i:j]
			i = j
			for i < n && isWS(content[i]) {
				i++
			}

			if i < n && content[i] == '{' {
				if word == "server" {
					inner, next := extractBlock(content, i+1)
					blocks = append(blocks, inner)
					i = next
				} else {
					i = skipBlock(content, i)
				}
			} else if i < n && content[i] == ';' {
				i++
			}
		}
	}
	return blocks
}

// skipBlock skips from the opening '{' to the matching '}', returning the
// position immediately after the closing brace.
func skipBlock(content string, i int) int {
	if i >= len(content) || content[i] != '{' {
		return i
	}
	i++
	depth := 1
	n := len(content)
	for i < n && depth > 0 {
		switch content[i] {
		case '{':
			depth++
		case '}':
			depth--
		}
		i++
	}
	return i
}

// extractBlock reads from i (the char after '{') to the matching '}' and
// returns (innerContent, posAfterClosingBrace).
func extractBlock(content string, i int) (string, int) {
	n := len(content)
	start := i
	depth := 1
	for i < n && depth > 0 {
		switch content[i] {
		case '{':
			depth++
		case '}':
			depth--
		}
		if depth > 0 {
			i++
		}
	}
	block := content[start:i]
	if i < n {
		i++ // consume '}'
	}
	return block, i
}

func isWS(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// parseBlock extracts directives from the content of a server{} block.
// proxy_pass is matched at any depth so location{} blocks are covered.
func parseBlock(content string) ParsedBlock {
	var block ParsedBlock

	// server_name — first directive only; filter wildcard "_"
	if m := reServerName.FindStringSubmatch(content); m != nil {
		for _, name := range strings.Fields(m[1]) {
			name = strings.TrimRight(name, ";")
			if name != "" && name != "_" {
				block.ServerNames = append(block.ServerNames, name)
			}
		}
	}

	// listen — collect all unique ports; detect ssl flag
	seen := map[int]bool{}
	for _, m := range reListen.FindAllStringSubmatch(content, -1) {
		for _, f := range strings.Fields(m[1]) {
			f = strings.TrimRight(f, ";")
			switch f {
			case "ssl":
				block.SSLEnabled = true
			case "http2", "default_server", "ipv6only=on", "ipv6only=off":
				// ignore flags
			default:
				// strip IPv6 brackets and get port after last ':'
				if idx := strings.LastIndexByte(f, ':'); idx >= 0 {
					f = f[idx+1:]
				}
				if port, err := strconv.Atoi(f); err == nil && !seen[port] {
					block.ListenPorts = append(block.ListenPorts, port)
					seen[port] = true
				}
			}
		}
	}

	// ssl_certificate — first match implies SSL
	if m := reSSLCert.FindStringSubmatch(content); m != nil {
		block.CertPath = strings.TrimRight(strings.TrimSpace(m[1]), ";")
		if block.CertPath != "" {
			block.SSLEnabled = true
		}
	}

	// proxy_pass — first match at any depth within the block
	if m := reProxyPass.FindStringSubmatch(content); m != nil {
		block.ProxyPass = strings.TrimRight(strings.TrimSpace(m[1]), ";")
	}

	return block
}

// proxyPassParts extracts host and port from a proxy_pass URL like
// "http://127.0.0.1:8080". Returns port=80/443 for scheme defaults.
func proxyPassParts(proxyPass string) (host string, port int) {
	u, err := url.Parse(proxyPass)
	if err != nil {
		return "", 0
	}
	host = u.Hostname()
	portStr := u.Port()
	if portStr == "" {
		switch u.Scheme {
		case "http":
			return host, 80
		case "https":
			return host, 443
		}
		return host, 0
	}
	p, _ := strconv.Atoi(portStr)
	return host, p
}
