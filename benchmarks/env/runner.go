package env

import (
	"os"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
)

func Runner() *schema.RunnerInfo {
	provider := "local"
	if os.Getenv("GITHUB_ACTIONS") == "true" && os.Getenv("RUNNER_ENVIRONMENT") == schema.HostedProvider {
		provider = schema.HostedProvider
	}
	return &schema.RunnerInfo{Provider: provider, Image: os.Getenv("ImageOS"), ImageVersion: os.Getenv("ImageVersion"),
		RunID: os.Getenv("GITHUB_RUN_ID"), Job: os.Getenv("GITHUB_JOB"), Repetition: os.Getenv("BENCHMARK_REPETITION"),
		Attempt: os.Getenv("GITHUB_RUN_ATTEMPT"), Repository: os.Getenv("GITHUB_REPOSITORY"),
		Protocol: schema.StartupProtocol}
}
