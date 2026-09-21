package telemetry

import (
	"slices"
	"testing"
)

func TestDefinition_DefaultBuckets(t *testing.T) {
	tests := []struct {
		name string
		opts []MetricOption
		want []float64
	}{
		{"seconds", []MetricOption{WithUnit("s")}, serverDurationBuckets},
		{"bytes", []MetricOption{WithUnit("By")}, sizeBuckets},
		{"other units keep the SDK defaults", []MetricOption{WithUnit("{result}")}, nil},
		{"explicit buckets win", []MetricOption{WithUnit("s"), WithBuckets(1, 2)}, []float64{1, 2}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := newDefinition("x", tt.opts).boundaries(); !slices.Equal(got, tt.want) {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}
