package patterns

import (
	"WalletTools/pkg/config"
	"regexp"
	"strings"
	"sync"
)

type MatchResult struct {
	Kind  string // symmetric|specific|edges|regexp
	Index int
	Final bool
}

func MatchAddress(cfg *config.PatternsConfig, addr string) *MatchResult {
	full := strings.TrimSpace(addr)
	body := strings.TrimPrefix(strings.TrimPrefix(full, "0x"), "0X")
	if !cfg.CaseSensitive {
		full = strings.ToLower(full)
		body = strings.ToLower(body)
	}

	// symmetric
	if len(cfg.Symmetric) > 0 {
		for i, p := range cfg.Symmetric {
			if matchSymmetric(body, p.Prefix, p.Suffix, cfg.CaseSensitive) {
				return &MatchResult{Kind: "symmetric", Index: i, Final: p.Final}
			}
		}
	}

	// specific
	for i, p := range cfg.Specific {
		pre := p.Prefix
		suf := p.Suffix
		if !cfg.CaseSensitive {
			pre = strings.ToLower(pre)
			suf = strings.ToLower(suf)
		}
		if strings.HasPrefix(body, pre) && strings.HasSuffix(body, suf) {
			return &MatchResult{Kind: "specific", Index: i, Final: p.Final}
		}
	}

	// edges
	if cfg.Edges.MinCount > 0 {
		if cfg.Edges.Side == "prefix" || cfg.Edges.Side == "any" {
			r := runLenPrefix(body)
			if r >= cfg.Edges.MinCount {
				return &MatchResult{Kind: "edges", Index: 0, Final: cfg.Edges.Final}
			}
		}
		if cfg.Edges.Side == "suffix" || cfg.Edges.Side == "any" {
			r := runLenSuffix(body)
			if r >= cfg.Edges.MinCount {
				return &MatchResult{Kind: "edges", Index: 0, Final: cfg.Edges.Final}
			}
		}
	}

	// regexp
	for i, rp := range cfg.Regexp {
		re, err := compiledRegexp(rp.Pattern, cfg.CaseSensitive)
		if err != nil {
			continue
		}
		if re.MatchString(full) {
			return &MatchResult{Kind: "regexp", Index: i, Final: rp.Final}
		}
	}
	return nil
}

var regexpCache sync.Map

func compiledRegexp(pattern string, caseSensitive bool) (*regexp.Regexp, error) {
	if !caseSensitive {
		pattern = "(?i)" + pattern
	}
	if re, ok := regexpCache.Load(pattern); ok {
		return re.(*regexp.Regexp), nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	actual, _ := regexpCache.LoadOrStore(pattern, re)
	return actual.(*regexp.Regexp), nil
}

func runLenPrefix(s string) int {
	if s == "" {
		return 0
	}
	first := s[0]
	n := 1
	for i := 1; i < len(s); i++ {
		if s[i] == first {
			n++
		} else {
			break
		}
	}
	return n
}

func runLenSuffix(s string) int {
	if s == "" {
		return 0
	}
	last := s[len(s)-1]
	n := 1
	for i := len(s) - 2; i >= 0; i-- {
		if s[i] == last {
			n++
		} else {
			break
		}
	}
	return n
}

func matchSymmetric(addr, pre, suf string, caseSensitive bool) bool {
	if !caseSensitive {
		pre = strings.ToLower(pre)
		suf = strings.ToLower(suf)
	}
	if len(addr) < len(pre)+len(suf) {
		return false
	}

	prefixPart := addr[:len(pre)]
	suffixPart := addr[len(addr)-len(suf):]

	bindings := make(map[byte]byte, 2)
	return matchPatternPart(pre, prefixPart, bindings) &&
		matchPatternPart(suf, suffixPart, bindings)
}

func matchPatternPart(pattern, part string, bindings map[byte]byte) bool {
	if len(pattern) != len(part) {
		return false
	}
	if !hasOnlyPlaceholders(pattern) {
		return pattern == part
	}
	pattern = strings.ToUpper(pattern)
	for i := 0; i < len(pattern); i++ {
		placeholder := pattern[i]
		if bound, ok := bindings[placeholder]; ok {
			if part[i] != bound {
				return false
			}
			continue
		}
		bindings[placeholder] = part[i]
	}
	return true
}

func hasOnlyPlaceholders(pattern string) bool {
	if pattern == "" {
		return false
	}
	pattern = strings.ToUpper(pattern)
	for i := 0; i < len(pattern); i++ {
		if pattern[i] != 'X' && pattern[i] != 'Y' {
			return false
		}
	}
	return true
}
