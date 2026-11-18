package sales

import "testing"

func TestSatangRoundsHalfUpToTwoDecimals(t *testing.T) {
	cases := []struct{ in, want float64 }{
		{55.0002, 55.00}, {55.005, 55.01}, {8.6415, 8.64}, {0.004999, 0.00}, {114.81, 114.81}, {0, 0},
	}
	for _, c := range cases {
		if got := satang(c.in); got != c.want {
			t.Fatalf("satang(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}
