package runner

import (
	"path/filepath"
	"strconv"
	"strings"
)

func hostQualifications(host map[string]string, cfg ExperimentConfig) []string {
	var warnings []string
	frequencyKnown, variableGovernor, siblingsKnown, sharedSiblings := false, false, false, false
	for path, value := range host {
		if filepath.Base(path) == "scaling_governor" {
			frequencyKnown = true
			variableGovernor = variableGovernor || value != "performance"
		}
		if filepath.Base(path) != "thread_siblings_list" {
			continue
		}
		for _, cpu := range cfg.Target.Affinity {
			if !strings.Contains(path, "/cpu"+strconv.Itoa(cpu)+"/") {
				continue
			}
			siblingsKnown = true
			for _, other := range cfg.Generator.Affinity {
				sharedSiblings = sharedSiblings || cpuInList(other, value)
			}
		}
	}
	if !frequencyKnown || variableGovernor {
		warnings = append(warnings, "noisy-environment: fixed performance governor not established")
	}
	if len(cfg.Target.Affinity) > 0 && len(cfg.Generator.Affinity) > 0 && (!siblingsKnown || sharedSiblings) {
		warnings = append(warnings, "noisy-environment: target/generator physical-core separation not established")
	}
	return warnings
}

func cpuInList(cpu int, list string) bool {
	for _, part := range strings.Split(list, ",") {
		low, high, rangePresent := strings.Cut(strings.TrimSpace(part), "-")
		first, err := strconv.Atoi(low)
		if err != nil {
			continue
		}
		last := first
		if rangePresent {
			last, err = strconv.Atoi(high)
		}
		if err == nil && cpu >= first && cpu <= last {
			return true
		}
	}
	return false
}
