// Package research377 contains an isolated fixed-sample tail-risk research POC.
// It does not change the benchmark runner or production dispatch behavior.
package research377

import (
	"errors"
	"math"
)

const (
	MaximumSamples = 100000
	MaximumFamily  = 1000
	bisectionSteps = 64
	tailSplit      = 2
	floatTolerance = 1e-15
)

// Interval is a closed interval. Probability bounds use the exact binomial
// construction, evaluated with float64 arithmetic, rather than a normal approximation.
type Interval struct {
	Low  float64 `json:"low"`
	High float64 `json:"high"`
}

// BinomialInterval inverts equal-tailed binomial tests (Clopper-Pearson).
// For independent identically distributed Bernoulli observations and a fixed
// sample size, coverage is at least 1-alpha in exact arithmetic.
func BinomialInterval(events, samples int, alpha float64) (Interval, error) {
	if samples < 1 || samples > MaximumSamples || events < 0 || events > samples || !validAlpha(alpha) {
		return Interval{}, errors.New("invalid event count, sample cap or alpha")
	}
	tail := alpha / tailSplit
	interval := Interval{Low: 0, High: 1}
	if events > 0 {
		interval.Low = invertTail(samples, samples-events, tail, true)
	}
	if events < samples {
		interval.High = invertTail(samples, events, tail, false)
	}
	return interval, nil
}

// The lower endpoint solves P_p(X >= events)=tail through symmetry; the upper
// solves P_p(X <= events)=tail. Bisection returns an outward-rounded bracket.
func invertTail(samples, events int, tail float64, lower bool) float64 {
	left, right := 0.0, 1.0
	for range bisectionSteps {
		midpoint := left + (right-left)/tailSplit
		if binomialCDF(samples, events, midpoint) > tail {
			left = midpoint
		} else {
			right = midpoint
		}
	}
	if lower {
		return math.Max(0, math.Nextafter(1-right, math.Inf(-1)))
	}
	return math.Min(1, math.Nextafter(right, math.Inf(1)))
}

// Sum from the boundary toward the smaller terms; symmetry avoids subtracting
// tiny probabilities when evaluating the tail being inverted.
func binomialCDF(samples, events int, probability float64) float64 {
	if events < 0 {
		return 0
	}
	if events >= samples || probability == 0 {
		return 1
	}
	if probability == 1 {
		return 0
	}
	if float64(events) >= float64(samples)*probability {
		return 1 - binomialSmallTail(samples, samples-events-1, 1-probability)
	}
	return binomialSmallTail(samples, events, probability)
}

func binomialSmallTail(samples, events int, probability float64) float64 {
	nFactorial, _ := math.Lgamma(float64(samples + 1))
	kFactorial, _ := math.Lgamma(float64(events + 1))
	restFactorial, _ := math.Lgamma(float64(samples - events + 1))
	logBoundary := nFactorial - kFactorial - restFactorial +
		float64(events)*math.Log(probability) + float64(samples-events)*math.Log1p(-probability)
	total, term := 1.0, 1.0
	for k := events; k > 0; k-- {
		term *= float64(k) * (1 - probability) / (float64(samples-k+1) * probability)
		total += term
		if term <= total*floatTolerance {
			break
		}
	}
	return math.Min(1, math.Exp(logBoundary)*total)
}

func validAlpha(alpha float64) bool {
	return !math.IsNaN(alpha) && alpha >= 1e-9 && alpha < 1
}
