package sales

import (
	"testing"
	"time"
)

func TestEachCommandHasOneEffectOnTheCustomersAccount(t *testing.T) {
	at := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		got  accountEffect
		want accountEffect
	}{
		{"a sale is spent and is a visit", saleEffect(250, at), accountEffect{spend: 250, visit: at}},
		{"a return takes the refund back, not a visit", returnEffect(40), accountEffect{spend: -40}},
		{"a void takes back what is left after refunds", voidEffect(250, 40), accountEffect{spend: -210}},
		{"a void of a fully refunded bill changes nothing", voidEffect(250, 250), accountEffect{}},
		{"a void never adds spend back", voidEffect(250, 300), accountEffect{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Fatalf("got %+v, want %+v", c.got, c.want)
			}
		})
	}
	if !voidEffect(100, 100).none() || saleEffect(0, at).none() {
		t.Fatal("none() must mean no spend and no visit")
	}
}
