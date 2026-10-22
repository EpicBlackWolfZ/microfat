# Fortio parser fixtures

`fortio-1.76.0.json` is real output captured from the pinned Fortio binary built by
`scripts/benchmark-tools.sh`, against the repository's `server.Handler(server.Defaults())`
on an ephemeral loopback HTTP server. The load arguments were:

```text
load -qps 1000 -c 16 -t 1s -r 0.00001 -uniform -nocatchup -allow-initial-errors -p 50,75,90,95,99,99.9 -json - URL/mixed
```

This fixture exercises parsing and orchestration; its timings are not release performance evidence.
The earlier `fortio-1.75.2.json` remains an unchanged historical capture.
