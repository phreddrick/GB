package racegen

import (
	"fmt"
	"math"
	"strings"
)

type ValidationError struct {
	Code    string `json:"code"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Errors is ordered by field/rule evaluation, deterministic, and contains no
// raw user input. It implements error for callers that need an error boundary.
type Errors []ValidationError

func (e Errors) Error() string {
	parts := make([]string, len(e))
	for i, v := range e {
		parts[i] = v.Field + ": " + v.Message
	}
	return strings.Join(parts, "; ")
}

func finite(v float64) bool       { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func attributeField(i int) string { return "attributes." + AttributeDefinitions()[i].Key }
func sectorField(i int) string    { return fmt.Sprintf("sector_compat[%d]", i) }
func overflow(field string) Errors {
	return Errors{{"COST_OVERFLOW", field, "cost exceeds defined original-C integer arithmetic"}}
}

func checkNumbers(s Spec) Errors {
	var errs Errors
	if s.RaceType != Normal && s.RaceType != Metamorph {
		errs = append(errs, ValidationError{"OUT_OF_RANGE", "race_type", "unknown race type"})
	}
	if s.Homeworld < Earth || s.Homeworld >= HomeworldCount {
		errs = append(errs, ValidationError{"OUT_OF_RANGE", "homeworld", "unknown homeworld"})
	}
	check := func(field string, v, min, max float64) {
		if !finite(v) {
			errs = append(errs, ValidationError{"NOT_FINITE", field, "must be finite"})
		} else if v < min || v > max {
			errs = append(errs, ValidationError{"OUT_OF_RANGE", field, fmt.Sprintf("must be between %g and %g", min, max)})
		}
	}
	for i, v := range s.Attributes.Values() {
		d := AttributeDefinitions()[i]
		check(attributeField(i), v, d.Minimum, d.Maximum)
	}
	for i, v := range s.SectorCompat {
		check(sectorField(i), v, 0, 100)
	}
	return errs
}

// Validate checks complete design rules, including budget. It rejects integral
// fractions rather than C's in-place truncation and checks water too (C's loop
// accidentally starts at sector 1). Decimal traits are not silently quantized.
// No credential, address, God/Guest privilege, or placement policy is implied.
func Validate(s Spec) Errors {
	errs := checkNumbers(s)
	if len(errs) > 0 {
		return errs
	}
	add := func(code, field, msg string) { errs = append(errs, ValidationError{code, field, msg}) }
	if strings.TrimSpace(s.Name) == "" {
		add("REQUIRED", "name", "use a non-empty name")
	}
	for i, v := range s.Attributes.Values() {
		d := AttributeDefinitions()[i]
		if d.Kind == Boolean && v != 0 && v != 1 {
			add("BOOLEAN", attributeField(i), "must be 0 or 1")
		} else if d.Kind == Integer && v != math.Trunc(v) {
			add("INTEGER", attributeField(i), "must be an integer")
		}
	}
	if s.RaceType == Normal {
		if s.Attributes.Absorb != 0 {
			add("NORMAL_ABILITY", "attributes.absorb", "normal races cannot absorb")
		}
		if s.Attributes.Pods != 0 {
			add("NORMAL_ABILITY", "attributes.pods", "normal races cannot make pods")
		}
	}
	if s.Homeworld != Jovian && s.SectorCompat[Plated] != 100 {
		add("PLATED_REQUIRED", sectorField(int(Plated)), "non-jovian races require 100% plated compatibility")
	}
	n := 0
	common := false
	for i, v := range s.SectorCompat {
		if v != math.Trunc(v) {
			add("INTEGER", sectorField(i), "must be an integer percentage")
		}
		if v != 0 {
			n++
		}
		if v == 100 && Homeworlds()[s.Homeworld].CommonSectors[i] {
			common = true
		}
		if s.Homeworld != Jovian && Sector(i) == Gas && v != 0 {
			add("GAS_FORBIDDEN", sectorField(i), "non-jovian races cannot have gas compatibility")
		}
		if s.Homeworld == Jovian && Sector(i) != Gas && v != 0 {
			add("JOVIAN_TERRAIN", sectorField(i), "jovian races can only have gas compatibility")
		}
	}
	if s.Homeworld != Jovian && n < 2 {
		add("TERRAIN_COUNT", "sector_compat", "non-jovian races require at least two sector types")
	}
	if !common {
		add("COMMON_TERRAIN", "sector_compat", "require 100% compatibility with a common homeworld sector")
	}
	b, costErrs := Cost(s)
	if len(costErrs) > 0 {
		errs = append(errs, costErrs...)
	} else if b.Remaining < 0 {
		add("OVER_BUDGET", "cost", "design exceeds the point budget")
	}
	return errs
}

// Validated returns the unchanged specification only if every design check
// succeeds. Callers must still enforce identity, permissions and placement.
func Validated(s Spec) (Spec, Errors) {
	if errs := Validate(s); len(errs) > 0 {
		return Spec{}, errs
	}
	return s, nil
}
