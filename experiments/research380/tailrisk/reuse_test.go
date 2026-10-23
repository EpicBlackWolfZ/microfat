package research377

// Test helpers from the original calibration component; the reused inference
// source files analysis.go and bounds.go remain byte-identical to #377.
const (
	DefaultAlpha  = 0.05
	DefaultMargin = 0.01
)

func contains(interval Interval, value float64) bool {
	return interval.Low <= value && value <= interval.High
}
