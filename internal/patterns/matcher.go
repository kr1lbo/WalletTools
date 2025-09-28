package patterns

import (
	"WalletTools/pkg/config"
	"regexp"
	"strings"
)

type MatchResult struct {
	Kind  string // symmetric|specific|edges|regexp
	Index int
	Final bool
}

func MatchAddress(cfg *config.PatternsConfig, addr string) *MatchResult {
	check := addr
	if !cfg.CaseSensitive {
		check = strings.ToLower(check)
	}

	// symmetric
	if len(cfg.Symmetric) > 0 {
		for i, p := range cfg.Symmetric {
			if matchSymmetric(check, p.Prefix, p.Suffix) {
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
		if strings.HasPrefix(check, pre) && strings.HasSuffix(check, suf) {
			return &MatchResult{Kind: "specific", Index: i, Final: p.Final}
		}
	}

	// edges
	if cfg.Edges.MinCount > 0 {
		if cfg.Edges.Side == "prefix" || cfg.Edges.Side == "any" {
			r := runLenPrefix(check)
			if r >= cfg.Edges.MinCount {
				return &MatchResult{Kind: "edges", Index: 0, Final: cfg.Edges.Final}
			}
		}
		if cfg.Edges.Side == "suffix" || cfg.Edges.Side == "any" {
			r := runLenSuffix(check)
			if r >= cfg.Edges.MinCount {
				return &MatchResult{Kind: "edges", Index: 0, Final: cfg.Edges.Final}
			}
		}
	}

	// regexp
	for i, rp := range cfg.Regexp {
		pat := rp.Pattern
		if !cfg.CaseSensitive {
			pat = "(?i)" + pat
		}
		re, err := regexp.Compile(pat)
		if err != nil {
			continue
		}
		if re.MatchString(check) {
			return &MatchResult{Kind: "regexp", Index: i, Final: rp.Final}
		}
	}
	return nil
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

func matchSymmetric(addr, pre, suf string) bool {
	if len(addr) < len(pre)+len(suf) {
		return false
	}

	prefixPart := addr[:len(pre)]
	suffixPart := addr[len(addr)-len(suf):]

	checkPattern := func(pattern, part string) (byte, bool) {
		if len(pattern) != len(part) {
			return 0, false
		}
		var symbol byte
		for i := 0; i < len(pattern); i++ {
			switch pattern[i] {
			case 'X', 'Y':
				if symbol == 0 {
					symbol = part[i]
				} else if part[i] != symbol {
					return 0, false
				}
			default:
				// Любой другой символ запрещён
				return 0, false
			}
		}
		return symbol, true
	}

	symPre, okPre := checkPattern(pre, prefixPart)
	symSuf, okSuf := checkPattern(suf, suffixPart)
	if !okPre || !okSuf {
		return false
	}

	if strings.Contains(pre, "X") && strings.Contains(suf, "X") {
		return symPre == symSuf
	}
	if strings.Contains(pre, "Y") && strings.Contains(suf, "Y") {
		return symPre == symSuf
	}
	return true
}
