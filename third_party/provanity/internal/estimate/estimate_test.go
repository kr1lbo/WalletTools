package estimate

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/woomai/provanity/internal/vanity"
)

func TestForPattern(t *testing.T) {
	pattern, err := vanity.ParsePattern("leading:0:2")
	if err != nil {
		t.Fatalf("ParsePattern: %v", err)
	}

	got, err := ForPattern(pattern, 256)
	if err != nil {
		t.Fatalf("ForPattern: %v", err)
	}
	if math.Abs(got.Probability-(1.0/256.0)) > 0.0000001 {
		t.Fatalf("probability = %f", got.Probability)
	}
	if got.Expected.Seconds() < 0.99 || got.Expected.Seconds() > 1.01 {
		t.Fatalf("expected = %s", got.Expected)
	}
}

func TestForTronPattern(t *testing.T) {
	pattern, err := vanity.ParseTronPattern("prefix:AB")
	if err != nil {
		t.Fatalf("ParseTronPattern: %v", err)
	}

	got, err := ForPattern(pattern, 58*58)
	if err != nil {
		t.Fatalf("ForPattern: %v", err)
	}
	if math.Abs(got.Probability-(1.0/(58.0*58.0))) > 0.0000001 {
		t.Fatalf("probability = %f", got.Probability)
	}
	if got.Expected.Seconds() < 0.99 || got.Expected.Seconds() > 1.01 {
		t.Fatalf("expected = %s", got.Expected)
	}
}

func TestForPatternRejectsBadHashrate(t *testing.T) {
	pattern, err := vanity.ParsePattern("pattern:dead")
	if err != nil {
		t.Fatalf("ParsePattern: %v", err)
	}
	if _, err := ForPattern(pattern, 0); err == nil {
		t.Fatal("expected bad hashrate error")
	}
}

// With alphabetSize 16, targetScore 1 and hashrate 16 the rate is exactly one
// hit per second, so each milestone time is -ln(1-q) seconds.
func TestForTargetLaddersThroughMilestones(t *testing.T) {
	got, err := ForTarget(1, 16, 16, 0)
	if err != nil {
		t.Fatalf("ForTarget: %v", err)
	}
	if got.Reached || got.Probability != 0.25 {
		t.Fatalf("at start = %#v", got)
	}
	if got.Remaining <= 0 {
		t.Fatalf("remaining should be positive at start: %s", got.Remaining)
	}

	got, err = ForTarget(1, 16, 16, got.Total+time.Millisecond)
	if err != nil {
		t.Fatalf("ForTarget after p25: %v", err)
	}
	if got.Probability != 0.50 {
		t.Fatalf("probability = %v, want 0.50", got.Probability)
	}
	if got.Remaining <= 0 {
		t.Fatalf("remaining should still be positive before p50: %s", got.Remaining)
	}
}

func TestForTargetSwitchesToLiveChancePastFinalMilestone(t *testing.T) {
	got, err := ForTarget(1, 16, 16, 24*time.Hour)
	if err != nil {
		t.Fatalf("ForTarget: %v", err)
	}
	if !got.Reached {
		t.Fatalf("expected live mode, got %#v", got)
	}
	if got.Probability <= 0.90 || got.Probability >= 1 {
		t.Fatalf("live probability out of range: %v", got.Probability)
	}
	if got.Remaining != 0 {
		t.Fatalf("remaining should be zero in live mode: %s", got.Remaining)
	}
}

func TestForTargetLiveChanceClimbsButStaysBelowOne(t *testing.T) {
	early, err := ForTarget(1, 16, 16, 10*time.Second)
	if err != nil {
		t.Fatalf("ForTarget early: %v", err)
	}
	late, err := ForTarget(1, 16, 16, 100*time.Second)
	if err != nil {
		t.Fatalf("ForTarget late: %v", err)
	}
	if !early.Reached || !late.Reached {
		t.Fatalf("expected live mode: early=%#v late=%#v", early, late)
	}
	if !(late.Probability > early.Probability) {
		t.Fatalf("probability should climb: %v -> %v", early.Probability, late.Probability)
	}
	if late.Probability >= 1 {
		t.Fatalf("probability must stay below 1: %v", late.Probability)
	}
}

// Current is the live cumulative chance regardless of mode: below the next
// milestone while laddering, equal to Probability once live, and monotonically
// climbing. It drives the 0→100% progress bar.
func TestForTargetCurrentTracksCumulativeChance(t *testing.T) {
	// rate = 16^-1 * 16 = 1 hit/sec, so t90 = -ln(0.1) ≈ 2.30s.
	start, err := ForTarget(1, 16, 16, 0)
	if err != nil {
		t.Fatalf("ForTarget: %v", err)
	}
	if start.Current != 0 {
		t.Fatalf("current at start = %v, want 0", start.Current)
	}
	if start.Current >= start.Probability {
		t.Fatalf("milestone-mode current %v should sit below the milestone %v", start.Current, start.Probability)
	}

	a, _ := ForTarget(1, 16, 16, 1*time.Second)
	b, _ := ForTarget(1, 16, 16, 2*time.Second)
	if !(b.Current > a.Current) {
		t.Fatalf("current should climb with elapsed: %v -> %v", a.Current, b.Current)
	}

	live, _ := ForTarget(1, 16, 16, 5*time.Second)
	if !live.Reached {
		t.Fatalf("expected live mode at 5s")
	}
	if math.Abs(live.Current-live.Probability) > 1e-12 {
		t.Fatalf("live current %v should equal probability %v", live.Current, live.Probability)
	}
	if live.Current >= 1 {
		t.Fatalf("current must stay below 1: %v", live.Current)
	}
}

func TestForTargetRejectsBadInputs(t *testing.T) {
	if _, err := ForTarget(0, 16, 16, 0); err == nil {
		t.Fatal("expected target score error")
	}
	if _, err := ForTarget(1, 1, 16, 0); err == nil {
		t.Fatal("expected alphabet size error")
	}
	if _, err := ForTarget(1, 16, 0, 0); err == nil {
		t.Fatal("expected hashrate error")
	}
}

// A 16-character hex target at a fast hashrate is effectively never; it must
// stay in milestone mode with a capped, readable remaining time.
func TestForTargetHardTargetCapsRemaining(t *testing.T) {
	got, err := ForTarget(16, 16, 1e9, time.Hour)
	if err != nil {
		t.Fatalf("ForTarget: %v", err)
	}
	if got.Reached {
		t.Fatalf("hard target should still be in milestone mode: %#v", got)
	}
	if FormatDuration(got.Remaining) != ">100y" {
		t.Fatalf("remaining = %s, want >100y", FormatDuration(got.Remaining))
	}
}

func TestFormatChance(t *testing.T) {
	cases := []struct {
		p    float64
		want string
	}{
		{0.25, "25%"},
		{0.50, "50%"},
		{0.75, "75%"},
		{0.90, "90%"},
		{0.95, "95%"},
		{0.99, "99%"},
		{0.991, "99.1%"},
		{0.999, "99.9%"},
		{0.9999, "99.99%"},
		{0.99999, "99.999%"},
	}
	for _, c := range cases {
		if got := FormatChance(c.p); got != c.want {
			t.Errorf("FormatChance(%v) = %q, want %q", c.p, got, c.want)
		}
	}
}

func TestFormatChanceNeverReachesHundred(t *testing.T) {
	for _, p := range []float64{0.999999999, 0.99999999999999, 1.0, 1.5} {
		got := FormatChance(p)
		if strings.Contains(got, "100") {
			t.Errorf("FormatChance(%v) = %q, must stay below 100%%", p, got)
		}
	}
}

func TestFormatDurationYears(t *testing.T) {
	// 800 days = 2 years (730 days) + 70 days.
	if got := FormatDuration(800 * 24 * time.Hour); got != "2y070d" {
		t.Fatalf("FormatDuration(800d) = %s, want 2y070d", got)
	}
	if got := FormatDuration(time.Duration(math.MaxInt64)); got != ">100y" {
		t.Fatalf("FormatDuration(max) = %s, want >100y", got)
	}
}
