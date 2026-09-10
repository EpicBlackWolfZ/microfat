package schema

const StartupProtocol = "helper-spawn-to-ready-line-v2"
const HostedProvider = "github-hosted"

// RunnerInfo records reported VM identity; it is not an attestation of physical hardware.
type RunnerInfo struct {
	Provider     string `json:"provider"`
	Image        string `json:"image"`
	ImageVersion string `json:"image_version"`
	RunID        string `json:"run_id"`
	Job          string `json:"job"`
	Repetition   string `json:"repetition"`
	Attempt      string `json:"attempt"`
	Repository   string `json:"repository"`
	Protocol     string `json:"protocol"`
}
