. as $r |
def app($e): first($r.request.applications[] | select(.family == $e.family));
def scenario($e): first($r.request.scenarios[] | select(.name == $e.scenario));
def valid_episode:
  . as $e | app($e) as $a | scenario($e) as $s |
  (.launches|length) == ($s.launch_offsets_ns|length) and
  .maximum_scheduled_to_ready_ns == ([.launches[].scheduled_to_ready_ns]|max) and
  .foreground_cpu_ns == ([.launches[].process.cpu_ns]|add) and
  .deployment_to_first_ready_ns == (.initial_deployment_delay_ns + ([.launches[].ready_ns]|min)) and
  .deployment_plus_foreground_span_ns == (.initial_deployment_delay_ns + .foreground_span_ns) and
  .preparation_plus_foreground_cpu_ns == (.foreground_cpu_ns + (.preparation.cpu_ns // 0)) and
  .logical_retained_byte_seconds == (.cache_bytes * $s.logical_retention_horizon_ns / 1000000000) and
  (all(.launches[];
    .scheduled_ns == $s.launch_offsets_ns[.index] and
    .scheduled_to_ready_ns == (.ready_ns-.scheduled_ns) and .started_ns>=.scheduled_ns and
    .process.exit_code==0 and (.process.timed_out|not) and (.failed|not) and
    .process.stdout_sha256 == $a.expected_stdout_sha256 and
    .dispatch.selected_variant == $a.selected_level and
    .dispatch.selected_sha256 == $a.selected_payload_sha256 and
    .dispatch.selected_size_bytes == $a.selected_payload_bytes and
    .dispatch.exec_mode == (if $e.arm=="never-prewarm" then "memfd" else "cache" end)
  ));
{
 schema_version:1,
 auditor:"independent jq recomputation from immutable raw confirmation report",
 producer_revision:$r.producer_revision,
 request_sha256:$r.request_sha256,
 episodes:($r.episodes|length),
 unique_episode_ids:([$r.episodes[].id]|unique|length),
 launch_count:([$r.episodes[].launches[]]|length),
 valid_episode_arithmetic_and_dispatch:(all($r.episodes[];valid_episode)),
 failures:([$r.episodes[]|select(.failed or .timed_out)]|length),
 group_sample_sizes:($r.episodes|group_by([.family,.scenario,.arm])|map(length)),
 dispatch_counts:([$r.episodes[].launches[].dispatch.exec_mode]|group_by(.)|map({mode:.[0],count:length})),
 groups:($r.episodes|group_by([.family,.scenario,.arm])|map({family:.[0].family,scenario:.[0].scenario,
   arm:.[0].arm,attempts:length,violations:map(select(.failed or .timed_out or .maximum_scheduled_to_ready_ns>$r.request.episode_slo_ns))|length}))
}
