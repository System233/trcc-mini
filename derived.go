// SPDX-License-Identifier: GPL-3.0-or-later
// Explicitly estimated composition; never label it measured wall power.
package main

import (
	"fmt"
	"math"
	"strings"
)

type derivedConfig struct {
	Operation string   `json:"operation"` // only sum (watts) is currently supported
	Sources   []string `json:"sources"`
	Offset    float64  `json:"offset,omitempty"` // user-provided estimated miscellaneous watts
}

func validateDerived(defined map[string]derivedConfig) error {
	if len(defined) > 32 {
		return fmt.Errorf("derived: maximum 32 definitions")
	}
	for name, d := range defined {
		if !strings.HasSuffix(name, ".estimated") || len(name) > 128 {
			return fmt.Errorf("derived key %q must end in .estimated to distinguish estimates from measured power", name)
		}
		if metricNativeUnit(name) != "" {
			return fmt.Errorf("derived %s conflicts with measured metric", name)
		}
		if d.Operation != "sum" || len(d.Sources) < 2 || len(d.Sources) > 16 {
			return fmt.Errorf("derived.%s requires operation=sum with 2..16 sources", name)
		}
		if math.IsNaN(d.Offset) || math.IsInf(d.Offset, 0) || d.Offset < 0 || d.Offset > 10000 {
			return fmt.Errorf("derived.%s.offset must be finite 0..10000 W", name)
		}
		seen := make(map[string]bool)
		for _, source := range d.Sources {
			if !supportedMetric(source, defined) {
				return fmt.Errorf("derived.%s: unknown source %q", name, source)
			}
			if seen[source] {
				return fmt.Errorf("derived.%s: repeated source %q would double-count power", name, source)
			}
			seen[source] = true
			if metricNativeUnit(source) != "" && !metricIsMeasuredPower(source) {
				return fmt.Errorf("derived.%s: only power metrics may be summed (%s)", name, source)
			}
		}
	}
	status := make(map[string]int)
	var visit func(string) error
	visit = func(name string) error {
		if status[name] == 1 {
			return fmt.Errorf("derived: dependency cycle at %s", name)
		}
		if status[name] == 2 {
			return nil
		}
		status[name] = 1
		for _, source := range defined[name].Sources {
			if _, isDerived := defined[source]; isDerived {
				if err := visit(source); err != nil {
					return err
				}
			}
		}
		status[name] = 2
		return nil
	}
	for name := range defined {
		if err := visit(name); err != nil {
			return err
		}
	}
	// Reject indirect double-counting as well. Summing an existing
	// CPU+GPU estimate and GPU power again looks numerically reasonable,
	// but it is not an honest whole-system estimate.
	for name := range defined {
		leaves := make(map[string]bool)
		var leafNames []string
		var visitLeaves func(string) error
		visitLeaves = func(source string) error {
			if spec, isDerived := defined[source]; isDerived {
				for _, dependency := range spec.Sources {
					if err := visitLeaves(dependency); err != nil {
						return err
					}
				}
				return nil
			}
			if leaves[source] {
				return fmt.Errorf("derived.%s: input %s would be counted more than once", name, source)
			}
			for _, other := range leafNames {
				if powerSourcesOverlap(source, other) {
					return fmt.Errorf("derived.%s: %s and %s overlap in power coverage", name, source, other)
				}
			}
			leaves[source] = true
			leafNames = append(leafNames, source)
			return nil
		}
		if err := visitLeaves(name); err != nil {
			return err
		}
	}
	return nil
}

func applyDerived(m *metrics, definitions map[string]derivedConfig) {
	if len(definitions) == 0 {
		return
	}
	if m.Extras == nil {
		m.Extras = make(map[string]sampleValue)
	}
	calculated := make(map[string]bool)
	var eval func(string) (float64, bool)
	eval = func(name string) (float64, bool) {
		if _, isDerived := definitions[name]; !isDerived {
			return readMetric(*m, name)
		}
		if calculated[name] {
			s := m.Extras[name]
			return s.Value, s.Valid
		}
		calculated[name] = true
		d := definitions[name]
		sum := d.Offset
		validSources := 0
		for _, source := range d.Sources {
			value, valid := eval(source)
			// A temporarily missing sensor must not poison a sum; even
			// nested derived values contribute only when genuinely valid.
			if !valid || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
				continue
			}
			validSources++
			sum += value
		}
		if validSources == 0 || math.IsNaN(sum) || math.IsInf(sum, 0) || sum < 0 {
			m.Extras[name] = sampleValue{}
			return 0, false
		}
		m.Extras[name] = sampleValue{Value: sum, Valid: true}
		return sum, true
	}
	for name := range definitions {
		eval(name)
	}
}
