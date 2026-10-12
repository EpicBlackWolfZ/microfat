package runner

import (
	"strconv"
	"strings"
)

func hostQualifications(host map[string]string, cfg ExperimentConfig) []string {
	var warnings []string
	frequencyKnown := len(cfg.Target.Affinity)+len(cfg.Generator.Affinity) > 0
	siblingsKnown, sharedSiblings := true, false
	for _, group := range []struct{ cpus, others []int }{
		{cfg.Target.Affinity, cfg.Generator.Affinity},
		{cfg.Generator.Affinity, cfg.Target.Affinity},
	} {
		for _, cpu := range group.cpus {
			governor := cpuHostValue(host, cpu, "cpufreq/scaling_governor")
			frequencyKnown = frequencyKnown && governor == "performance"
			siblings := cpuHostValue(host, cpu, "topology/thread_siblings_list")
			siblingsKnown = siblingsKnown && cpuInList(cpu, siblings)
			for _, other := range group.others {
				sharedSiblings = sharedSiblings || cpuInList(other, siblings)
			}
		}
	}
	if !frequencyKnown {
		warnings = append(warnings, "noisy-environment: fixed performance governor not established")
	}
	if len(cfg.Target.Affinity) > 0 && len(cfg.Generator.Affinity) > 0 && (!siblingsKnown || sharedSiblings) {
		warnings = append(warnings, "noisy-environment: target/generator physical-core separation not established")
	}
	return warnings
}

func cpuHostValue(host map[string]string, cpu int, file string) string {
	suffix := "/cpu" + strconv.Itoa(cpu) + "/" + file
	for path, value := range host {
		if strings.HasSuffix(path, suffix) {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func cpuInList(cpu int, list string) bool {
	found := false
	for part := range strings.SplitSeq(list, ",") {
		low, high, rangePresent := strings.Cut(strings.TrimSpace(part), "-")
		first, err := strconv.Atoi(low)
		if err != nil || first < 0 {
			return false
		}
		last := first
		if rangePresent {
			last, err = strconv.Atoi(high)
		}
		if err != nil || last < first {
			return false
		}
		found = found || (cpu >= first && cpu <= last)
	}
	return found
}
