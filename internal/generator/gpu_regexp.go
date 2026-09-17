package generator

import (
	"fmt"
	"regexp/syntax"
	"strings"
	"unicode"
)

const maxRegexpExpansions = 8192

type asciiSet [2]uint64
type regexpSequence []asciiSet

func compileGPURegexp(expr string, caseSensitive bool) (string, error) {
	parseExpr := expr
	if !caseSensitive {
		parseExpr = "(?i:" + expr + ")"
	}
	re, err := syntax.Parse(parseExpr, syntax.Perl)
	if err != nil {
		return "", fmt.Errorf("invalid regexp %q: %w", expr, err)
	}
	start, end := regexpAnchors(re)
	if !start {
		re = &syntax.Regexp{Op: syntax.OpConcat, Sub: []*syntax.Regexp{anyStar(), re}}
	}
	if !end {
		re = &syntax.Regexp{Op: syntax.OpConcat, Sub: []*syntax.Regexp{re, anyStar()}}
	}
	seqs, err := expandRegexp(re)
	if err != nil {
		return "", fmt.Errorf("regexp %q is not supported by CUDA: %w", expr, err)
	}
	var merged [40]uint16
	valid := 0
	for _, seq := range seqs {
		if len(seq) != 42 || !seq[0].has('0') || !(seq[1].has('x') || seq[1].has('X')) {
			continue
		}
		var masks [40]uint16
		ok := true
		for i := 0; i < 40; i++ {
			masks[i] = hexMask(seq[i+2])
			if masks[i] == 0 {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		valid++
		for i := range merged {
			merged[i] |= masks[i]
		}
	}
	if valid == 0 {
		return "", fmt.Errorf("expression cannot match a 42-character 0x address")
	}
	constrained := false
	parts := make([]string, 40)
	for i, mask := range merged {
		if mask != 0xffff {
			constrained = true
		}
		parts[i] = fmt.Sprintf("%04x", mask)
	}
	if !constrained {
		return "", fmt.Errorf("expression has no GPU-selective hexadecimal positions")
	}
	return "mask:" + strings.Join(parts, ","), nil
}

func anyStar() *syntax.Regexp {
	return &syntax.Regexp{Op: syntax.OpStar, Sub: []*syntax.Regexp{{Op: syntax.OpAnyCharNotNL}}}
}

func regexpAnchors(re *syntax.Regexp) (bool, bool) {
	if re.Op == syntax.OpCapture {
		return regexpAnchors(re.Sub[0])
	}
	if re.Op != syntax.OpConcat || len(re.Sub) == 0 {
		return re.Op == syntax.OpBeginText, re.Op == syntax.OpEndText
	}
	start, _ := regexpAnchors(re.Sub[0])
	_, end := regexpAnchors(re.Sub[len(re.Sub)-1])
	return start, end
}

func expandRegexp(re *syntax.Regexp) ([]regexpSequence, error) {
	switch re.Op {
	case syntax.OpEmptyMatch, syntax.OpBeginLine, syntax.OpEndLine, syntax.OpBeginText, syntax.OpEndText:
		return []regexpSequence{{}}, nil
	case syntax.OpCapture:
		return expandRegexp(re.Sub[0])
	case syntax.OpLiteral:
		seq := make(regexpSequence, len(re.Rune))
		for i, r := range re.Rune {
			seq[i] = literalSet(r, re.Flags&syntax.FoldCase != 0)
		}
		return []regexpSequence{seq}, nil
	case syntax.OpCharClass:
		return []regexpSequence{{classSet(re.Rune, re.Flags&syntax.FoldCase != 0)}}, nil
	case syntax.OpAnyCharNotNL, syntax.OpAnyChar:
		return []regexpSequence{{allASCII()}}, nil
	case syntax.OpConcat:
		out := []regexpSequence{{}}
		for _, sub := range re.Sub {
			right, err := expandRegexp(sub)
			if err != nil {
				return nil, err
			}
			out, err = concatSequences(out, right)
			if err != nil {
				return nil, err
			}
		}
		return out, nil
	case syntax.OpAlternate:
		var out []regexpSequence
		for _, sub := range re.Sub {
			part, err := expandRegexp(sub)
			if err != nil {
				return nil, err
			}
			out = append(out, part...)
			if len(out) > maxRegexpExpansions {
				return nil, fmt.Errorf("too many alternatives (limit %d)", maxRegexpExpansions)
			}
		}
		return out, nil
	case syntax.OpQuest, syntax.OpStar, syntax.OpPlus, syntax.OpRepeat:
		min, max := re.Min, re.Max
		switch re.Op {
		case syntax.OpQuest:
			min, max = 0, 1
		case syntax.OpStar:
			min, max = 0, 42
		case syntax.OpPlus:
			min, max = 1, 42
		}
		if max < 0 || max > 42 {
			max = 42
		}
		unit, err := expandRegexp(re.Sub[0])
		if err != nil {
			return nil, err
		}
		var out []regexpSequence
		current := []regexpSequence{{}}
		for n := 0; n <= max; n++ {
			if n >= min {
				out = append(out, cloneSequences(current)...)
				if len(out) > maxRegexpExpansions {
					return nil, fmt.Errorf("too many quantified alternatives (limit %d)", maxRegexpExpansions)
				}
			}
			if n != max {
				current, err = concatSequences(current, unit)
				if err != nil {
					return nil, err
				}
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("operator %s", re.Op)
	}
}

func concatSequences(left, right []regexpSequence) ([]regexpSequence, error) {
	out := make([]regexpSequence, 0)
	for _, a := range left {
		for _, b := range right {
			if len(a)+len(b) > 42 {
				continue
			}
			seq := make(regexpSequence, 0, len(a)+len(b))
			seq = append(seq, a...)
			seq = append(seq, b...)
			out = append(out, seq)
			if len(out) > maxRegexpExpansions {
				return nil, fmt.Errorf("too many expansions (limit %d)", maxRegexpExpansions)
			}
		}
	}
	return out, nil
}

func cloneSequences(in []regexpSequence) []regexpSequence {
	out := make([]regexpSequence, len(in))
	for i := range in {
		out[i] = append(regexpSequence(nil), in[i]...)
	}
	return out
}

func literalSet(r rune, fold bool) asciiSet {
	var set asciiSet
	set.add(r)
	if fold {
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			set.add(f)
		}
	}
	return set
}

func classSet(ranges []rune, fold bool) asciiSet {
	var set asciiSet
	for c := rune(0); c < 128; c++ {
		for i := 0; i+1 < len(ranges); i += 2 {
			if c >= ranges[i] && c <= ranges[i+1] {
				set.add(c)
				if fold {
					for f := unicode.SimpleFold(c); f != c; f = unicode.SimpleFold(f) {
						set.add(f)
					}
				}
				break
			}
		}
	}
	return set
}

func allASCII() asciiSet { return asciiSet{^uint64(0), ^uint64(0)} }
func (s *asciiSet) add(r rune) {
	if r >= 0 && r < 128 {
		s[r/64] |= uint64(1) << (r % 64)
	}
}
func (s asciiSet) has(c byte) bool { return s[c/64]&(uint64(1)<<(c%64)) != 0 }

func hexMask(set asciiSet) uint16 {
	var mask uint16
	for n := byte(0); n < 16; n++ {
		lo := byte("0123456789abcdef"[n])
		hi := byte("0123456789ABCDEF"[n])
		if set.has(lo) || set.has(hi) {
			mask |= uint16(1) << n
		}
	}
	return mask
}
