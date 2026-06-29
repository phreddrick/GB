package game

import "math/rand"

// doubleRand returns a uniform random float64 in [0, 1).
// Mirrors double_rand() in rand.c.
func doubleRand() float64 {
	return rand.Float64()
}

// intRand returns a random integer in [low, hi] inclusive.
// Mirrors int_rand() in rand.c.
func intRand(low, hi int) int {
	if hi <= low {
		return low
	}
	return low + rand.Intn(hi-low+1)
}

// roundRand rounds x to an integer stochastically: the fractional part is
// the probability of rounding up rather than down.
// Mirrors round_rand() in rand.c.
func roundRand(x float64) int {
	if x < 0 {
		return int(x)
	}
	floor := int(x)
	if doubleRand() > x-float64(floor) {
		return floor
	}
	return floor + 1
}

// rposneg returns either -1 or 1 with equal probability.
// Mirrors rposneg() in rand.c.
func rposneg() int {
	if rand.Intn(2) == 0 {
		return -1
	}
	return 1
}

// randFloat returns a random float64 in [low, high].
func randFloat(low, high float64) float64 {
	return low + rand.Float64()*(high-low)
}

// success returns true with the given percent chance (0–100).
// Mirrors the success() macro in vars.h.
func success(percent int) bool {
	if percent <= 0 {
		return false
	}
	if percent >= 100 {
		return true
	}
	return intRand(1, 100) <= percent
}
