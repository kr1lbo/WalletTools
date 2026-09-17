package estimate

import (
	"fmt"
	"math"
	"time"

	"github.com/woomai/provanity/internal/vanity"
)

type Estimate struct {
	Probability float64
	Expected    time.Duration
	P50         time.Duration
	P95         time.Duration
}

// TargetChance describes how likely a search is to have produced the full
// target pattern. Up to the 90% milestone it is forward-looking: Probability is
// one of the laddered milestones (25/50/75/90%) and Remaining counts down to the
// moment that milestone is reached. Once the search has run past the 90% mark it
// switches to live reporting: Reached is true, Probability is the actual
// cumulative chance of a hit by the elapsed time — which climbs toward 1 but
// never reaches it — and Remaining is zero.
//
// Current is always the live cumulative chance of a hit by now, independent of
// mode. In milestone mode it sits below the next milestone (and drives a 0→100%
// progress bar); in live mode it equals Probability.
type TargetChance struct {
	Probability float64
	Current     float64
	Total       time.Duration
	Remaining   time.Duration
	Reached     bool
}

// targetMilestones are the laddered confidence levels shown before a search
// crosses into live reporting. Kept in ascending order.
var targetMilestones = []float64{0.25, 0.50, 0.75, 0.90}

func ForPattern(pattern vanity.Pattern, hashrate float64) (Estimate, error) {
	if hashrate <= 0 {
		return Estimate{}, fmt.Errorf("hashrate must be greater than zero")
	}
	probability, err := Probability(pattern)
	if err != nil {
		return Estimate{}, err
	}

	denominator := probability * hashrate
	return Estimate{
		Probability: probability,
		Expected:    secondsDuration(1 / denominator),
		P50:         secondsDuration(math.Log(2) / denominator),
		P95:         secondsDuration(math.Log(20) / denominator),
	}, nil
}

// ForTarget estimates the chance of having matched the full target pattern.
// targetScore is the count of fixed characters in the pattern and alphabetSize
// the address alphabet size (16 for hex, 58 for Base58), so a single attempt
// matches with probability alphabetSize^-targetScore. elapsed is the total time
// the search has been running.
//
// While the search is still short of the 90% milestone the result is
// forward-looking — Probability is the next laddered milestone and Remaining is
// the time until it is reached. Past the 90% mark the result switches to live
// reporting (Reached=true): Probability is the cumulative chance of a hit by
// now, which approaches but never reaches 1.
func ForTarget(targetScore, alphabetSize int, hashrate float64, elapsed time.Duration) (TargetChance, error) {
	if targetScore <= 0 {
		return TargetChance{}, fmt.Errorf("target score must be greater than zero")
	}
	if alphabetSize <= 1 {
		return TargetChance{}, fmt.Errorf("alphabet size must be greater than one")
	}
	if hashrate <= 0 {
		return TargetChance{}, fmt.Errorf("hashrate must be greater than zero")
	}
	if elapsed < 0 {
		elapsed = 0
	}

	// rate is the expected number of full-target hits per second.
	rate := math.Pow(float64(alphabetSize), -float64(targetScore)) * hashrate
	// current is the live cumulative chance of a hit by now. P(hit by t) =
	// 1 - e^(-rate*t) climbs toward 1 but never reaches it; clamp it just below 1
	// so it is never reported as a certainty.
	current := 1 - math.Exp(-rate*elapsed.Seconds())
	if current > maxLiveChance {
		current = maxLiveChance
	}
	if current < 0 {
		current = 0
	}

	for _, q := range targetMilestones {
		total := quantileDuration(q, rate)
		if elapsed < total {
			return TargetChance{
				Probability: q,
				Current:     current,
				Total:       total,
				Remaining:   total - elapsed,
			}, nil
		}
	}

	// Past the final milestone the headline figure is the live cumulative chance
	// itself. Floor it at the last milestone so it never appears to slip
	// backwards across the switch.
	floor := targetMilestones[len(targetMilestones)-1]
	probability := current
	if probability < floor {
		probability = floor
	}
	return TargetChance{Probability: probability, Current: current, Reached: true}, nil
}

// maxLiveChance is the highest live probability ForTarget will report: high
// enough that the ticking display reaches its finest precision, but strictly
// below 1 so the search is never shown as a sure thing.
const maxLiveChance = 1 - 1e-12

func Probability(pattern vanity.Pattern) (float64, error) {
	switch pattern.Kind {
	case vanity.PatternPattern, vanity.PatternLeading:
		return math.Pow(16, -float64(pattern.Count)), nil
	case vanity.PatternTronPrefix, vanity.PatternTronSuffix:
		return math.Pow(58, -float64(pattern.Count)), nil
	default:
		return 0, fmt.Errorf("unsupported pattern kind %q", pattern.Kind)
	}
}

// FormatChance renders a probability in [0,1) as a percentage with just enough
// precision to stay below 100% and reveal the leading moving digit. A search
// that is "almost certainly" done therefore shows a number that keeps ticking
// up (90% → 99% → 99.9% → 99.99% …) instead of rounding to a misleading 100%.
func FormatChance(p float64) string {
	if p <= 0 {
		return "0%"
	}
	if p >= 1 {
		// Defensive: the live chance is always < 1, but never print a literal
		// 100% if floating-point underflow pushes the probability to exactly 1.
		p = 1 - 1e-9
	}

	const maxDecimals = 6
	gap := 100 * (1 - p) // percentage points still below 100
	// Pick the fewest decimals that reveal gap's leading digit. The small
	// epsilon snaps power-of-10 boundaries (e.g. 1-0.9999 computes to
	// 0.00999…) so they don't spuriously gain an extra decimal place.
	decimals := 0
	if d := int(math.Ceil(-math.Log10(gap) - 1e-9)); d > 0 {
		decimals = d
	}
	if decimals > maxDecimals {
		decimals = maxDecimals
	}

	value := p * 100
	// Never render a bare 100%: if the value would round up at this precision
	// (only possible once decimals hits maxDecimals), pin it to the largest
	// sub-100 value representable here.
	if ceiling := 100 - 0.5*math.Pow(10, -float64(decimals)); value >= ceiling {
		value = 100 - math.Pow(10, -float64(decimals))
	}
	return fmt.Sprintf("%.*f%%", decimals, value)
}

func FormatDuration(d time.Duration) string {
	if d < time.Second {
		return "<1s"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	const day = 24 * time.Hour
	if d < 365*day {
		days := int(d.Hours()) / 24
		hours := int(d.Hours()) % 24
		return fmt.Sprintf("%dd%02dh", days, hours)
	}
	// Hard targets can produce effectively-never estimates; keep them readable
	// instead of rendering a six-figure day count.
	years := int(d / (365 * day))
	if years >= 100 {
		return ">100y"
	}
	days := int((d % (365 * day)) / day)
	return fmt.Sprintf("%dy%03dd", years, days)
}

func quantileDuration(q, denominator float64) time.Duration {
	return secondsDuration(-math.Log(1-q) / denominator)
}

func secondsDuration(seconds float64) time.Duration {
	if math.IsInf(seconds, 0) || math.IsNaN(seconds) || seconds > float64(math.MaxInt64)/float64(time.Second) {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(seconds * float64(time.Second))
}
