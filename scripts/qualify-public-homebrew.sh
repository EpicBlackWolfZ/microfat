#!/usr/bin/env bash
# Verify the documented remote tap command after native authenticated lifecycle
# qualification. Operate only inside that qualification's disposable prefix.
set -euo pipefail
IFS=$'\n\t'

fail() { printf 'Public Homebrew qualification: %s\n' "$*" >&2; exit 1; }
[[ $# == 3 ]] || fail 'usage: qualify-public-homebrew.sh ABSOLUTE_QUALIFICATION_DIR TAG EXPECTED_TAP_SHA'
qual_root=$1
release_tag=$2
tap_source=$3
[[ ${qual_root} == /* && ${qual_root} != / && -d ${qual_root} && ! -L ${qual_root} ]] || fail 'invalid qualification directory'
[[ ${release_tag} =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ && ${tap_source} =~ ^[0-9a-f]{40}$ ]] || fail 'invalid tag or tap source'
(( EUID != 0 )) || fail 'run qualification as an ordinary user'
for required_tool in jq git sha256sum realpath stat cmp timeout; do
    command -v "${required_tool}" >/dev/null || fail "required tool not found: ${required_tool}"
done
qual_root=$(realpath -e -- "${qual_root}")
brew_prefix="${qual_root}/prefix"
brew_binary="${brew_prefix}/bin/brew"
verification="${qual_root}/published/verification.json"
[[ -x ${brew_binary} && -f ${verification} && ! -L ${verification} ]] || fail 'missing completed published qualification'
case "$(uname -m)" in
    x86_64) native_arch=amd64; native_tier=v1 ;;
    aarch64|arm64) native_arch=arm64; native_tier=v8.0 ;;
    *) fail 'native Linux amd64 or arm64 is required' ;;
esac
native_os=$(uname -s)
[[ ${native_os} == Linux ]] || fail 'native Linux is required'
release_version=${release_tag#v}
helper_name="microfat-install_${release_version}_linux_${native_arch}"
jq -e --arg tag "${release_tag}" --arg arch "${native_arch}" --arg helper "${helper_name}" '
    .tag == $tag and (.release_id | type == "number" and . > 0) and
    (.source | type == "string" and test("^[0-9a-f]{40}$")) and
    (.checksums_sha256 | type == "string" and test("^[0-9a-f]{64}$")) and
    (.recipe_sha256 | type == "string" and test("^[0-9a-f]{64}$")) and
    (.assets[$helper] | type == "string" and test("^[0-9a-f]{64}$")) and
    (.products[$arch] | keys == ["microfat", "microfat-stub", "microfat-stub-minimal"]) and
    (.products[$arch] | all(.[]; type == "string" and test("^[0-9a-f]{64}$")))
' "${verification}" >/dev/null || fail 'authenticated evidence does not describe the requested release'

evidence_root="${qual_root}/public-homebrew"
mkdir -m 700 -- "${evidence_root}"
cp -- "${verification}" "${evidence_root}/verification.json"
brew_environment=(env -i "PATH=${brew_prefix}/bin:/usr/bin:/bin" "HOME=${qual_root}/home"
    "XDG_CACHE_HOME=${qual_root}/cache" "HOMEBREW_CACHE=${qual_root}/brew-cache"
    "HOMEBREW_LOGS=${qual_root}/brew-logs" HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_NO_ANALYTICS=1
    HOMEBREW_NO_INSTALL_FROM_API=1 HOMEBREW_NO_ENV_HINTS=1 HOMEBREW_DEVELOPER=1
    HOMEBREW_NO_INSTALL_CLEANUP=1 HOMEBREW_NO_GITHUB_API=1 HOMEBREW_NO_COLOR=1 HOMEBREW_FORCE_VENDOR_RUBY=1)
application_environment=(env -i "PATH=${brew_prefix}/bin:/usr/bin:/bin" "HOME=${qual_root}/home"
    "XDG_CACHE_HOME=${qual_root}/cache" "MICROFAT_CACHE_DIR=${qual_root}/payload-cache")

run_recorded() {
    local expectation=$1 record=$2 command_status=0
    shift 2
    printf '%q ' "$@" >> "${evidence_root}/commands.log"
    printf '\n' >> "${evidence_root}/commands.log"
    timeout --signal=TERM --kill-after=10s 5m "$@" > "${evidence_root}/${record}" 2>&1 || command_status=$?
    cat -- "${evidence_root}/${record}" >> "${evidence_root}/commands.log"
    printf '\nexit: %s\n' "${command_status}" >> "${evidence_root}/commands.log"
    if [[ ${expectation} == success ]]; then
        [[ ${command_status} == 0 ]] || fail "${record} failed with exit ${command_status}"
    else
        (( command_status > 0 && command_status != 124 && command_status != 137 )) || fail "${record} did not refuse cleanly"
    fi
}

digest_file() {
    local result
    result=$(sha256sum < "$1")
    printf '%s' "${result%% *}"
}

for product in microfat microfat-stub microfat-stub-minimal; do
    [[ ! -e ${brew_prefix}/bin/${product} && ! -L ${brew_prefix}/bin/${product} ]] || fail 'fixture still has an installed product'
done
run_recorded success brew-version.log "${brew_environment[@]}" "${brew_binary}" --version
[[ $(< "${evidence_root}/brew-version.log") == 'Homebrew 7.0.2' ]] || fail 'unexpected Homebrew version'
brew_source=$(git -C "${brew_prefix}/Homebrew" rev-parse HEAD)
[[ ${brew_source} == 83c9802fb54c60a612d52b264808c099b39a0687 ]] || fail 'unexpected Homebrew source'
actual_prefix=$("${brew_environment[@]}" "${brew_binary}" --prefix)
[[ ${actual_prefix} == "${brew_prefix}" ]] || fail 'Homebrew prefix escaped the fixture'

payload_source="${qual_root}/published/dist/${helper_name}"
expected_helper=$(jq -er --arg helper "${helper_name}" '.assets[$helper]' "${verification}")
[[ -f ${payload_source} && ! -L ${payload_source} ]] || fail 'published payload helper is not a regular file'
actual_helper=$(digest_file "${payload_source}")
[[ ${actual_helper} == "${expected_helper}" ]] || fail 'published payload helper does not match authenticated evidence'
payload="${evidence_root}/payload-helper"
cp -- "${payload_source}" "${payload}"
chmod 700 -- "${payload}"
run_recorded success helper-version.log "${application_environment[@]}" "${payload}" --helper-version

unrelated="${brew_prefix}/bin/public-acceptance-unrelated"
cache_sentinel="${qual_root}/payload-cache/public-acceptance-unrelated"
workload_sentinel="${evidence_root}/unrelated-workload"
[[ ! -e ${unrelated} && ! -L ${unrelated} && ! -e ${cache_sentinel} && ! -L ${cache_sentinel} ]] || fail 'sentinel already exists'
mkdir -p -- "${qual_root}/payload-cache"
printf 'preserve unrelated binary\n' > "${unrelated}"
printf 'preserve unrelated cache\n' > "${cache_sentinel}"
printf 'preserve unrelated workload\n' > "${workload_sentinel}"

verify_installation() {
    local phase=$1 product resolved physical='' expected actual mode profile packed management_marker owner permissions refusal_text
    for product in microfat microfat-stub microfat-stub-minimal; do
        [[ -L ${brew_prefix}/bin/${product} ]] || fail "${product} is not linked by Homebrew"
        resolved=$(realpath -e -- "${brew_prefix}/bin/${product}")
        [[ ${resolved} == "${brew_prefix}/Caskroom/microfat/${release_version}/"* ]] || fail 'product escaped the versioned cask'
        if [[ -z ${physical} ]]; then physical=${resolved%/*}; fi
        [[ ${resolved%/*} == "${physical}" && -f ${resolved} ]] || fail 'products do not share one regular-file generation'
        owner=$(stat -c '%u' -- "${resolved}")
        [[ ${owner} == "${EUID}" ]] || fail 'product is not owned by the fixture user'
        expected=$(jq -er --arg arch "${native_arch}" --arg product "${product}" '.products[$arch][$product]' "${verification}")
        actual=$(digest_file "${resolved}")
        [[ ${actual} == "${expected}" ]] || fail "Homebrew changed ${product} bytes"
        printf '%s  %s\n' "${actual}" "${product}" >> "${evidence_root}/product-hashes-${phase}.txt"
    done
    management_marker="${physical}/microfat-distribution.json"
    [[ -f ${management_marker} && ! -L ${management_marker} ]] || fail 'invalid Homebrew ownership marker'
    permissions=$(stat -c '%a' -- "${management_marker}")
    [[ ${permissions} == 644 ]] || fail 'invalid Homebrew ownership marker permissions'
    jq -e '. == {schema: 1, owner: "homebrew"}' "${management_marker}" >/dev/null || fail 'incorrect Homebrew ownership'
    run_recorded success "version-${phase}.log" "${application_environment[@]}" microfat --version
    [[ $(< "${evidence_root}/version-${phase}.log") == *"${release_version}"* ]] || fail 'installed version is incorrect'
    for mode in memfd cache; do
        run_recorded success "detect-${phase}-${mode}.json" "${application_environment[@]}" "MICROFAT_EXEC_MODE=${mode}" \
            microfat detect --json
        jq -e . "${evidence_root}/detect-${phase}-${mode}.json" >/dev/null
        for profile in full minimal; do
            packed="${evidence_root}/packed-${phase}-${mode}-${profile}"
            run_recorded success "pack-${phase}-${mode}-${profile}.log" "${application_environment[@]}" \
                "MICROFAT_EXEC_MODE=${mode}" microfat pack --arch "${native_arch}" --stub-profile "${profile}" \
                --name public-homebrew-acceptance -v "${native_tier}=${payload}" -o "${packed}"
            run_recorded success "payload-${phase}-${mode}-${profile}.log" "${application_environment[@]}" \
                "MICROFAT_EXEC_MODE=${mode}" "${packed}" --helper-version
            cmp -- "${evidence_root}/helper-version.log" "${evidence_root}/payload-${phase}-${mode}-${profile}.log"
        done
        run_recorded success "update-${phase}-${mode}.json" "${application_environment[@]}" "MICROFAT_EXEC_MODE=${mode}" \
            microfat update --check --version 0.2.5 --json
        jq -e '.management == "homebrew" and .can_self_update == false' "${evidence_root}/update-${phase}-${mode}.json" >/dev/null
        run_recorded refusal "update-refusal-${phase}-${mode}.log" "${application_environment[@]}" \
            "MICROFAT_EXEC_MODE=${mode}" microfat update --version 0.2.5 --allow-downgrade
        refusal_text=$(< "${evidence_root}/update-refusal-${phase}-${mode}.log")
        [[ ${refusal_text} == *'brew upgrade microfat'* ]] || fail 'self-update refusal lacks Homebrew guidance'
    done
}

# Acquire the real public tap and verify its reviewed source before evaluating
# installation actions. The subsequent install uses the exact documented command.
run_recorded success tap.log "${brew_environment[@]}" "${brew_binary}" tap EpicBlackWolfZ/tap
tap_root=$("${brew_environment[@]}" "${brew_binary}" --repository EpicBlackWolfZ/tap)
[[ ${tap_root} == "${brew_prefix}/Homebrew/Library/Taps/epicblackwolfz/homebrew-tap" ]] || fail 'unexpected remote tap directory'
actual_tap_source=$(git -C "${tap_root}" rev-parse HEAD)
printf '%s\n' "${actual_tap_source}" > "${evidence_root}/tap-source.txt"
[[ ${actual_tap_source} == "${tap_source}" ]] || fail 'public tap source differs from the reviewed recipe commit'
tap_remote=$(git -C "${tap_root}" remote get-url origin)
[[ ${tap_remote,,} == https://github.com/epicblackwolfz/homebrew-tap || \
    ${tap_remote,,} == https://github.com/epicblackwolfz/homebrew-tap.git ]] || fail 'public tap has an unexpected origin'
actual_recipe=$(digest_file "${tap_root}/Casks/microfat.rb")
expected_recipe=$(jq -er '.recipe_sha256' "${verification}")
printf '%s  Casks/microfat.rb\n' "${actual_recipe}" > "${evidence_root}/cask.sha256"
[[ ${actual_recipe} == "${expected_recipe}" ]] || fail 'public recipe differs from authenticated generation'
run_recorded success install.log "${brew_environment[@]}" "${brew_binary}" install --cask EpicBlackWolfZ/tap/microfat
verify_installation install
run_recorded success reinstall.log "${brew_environment[@]}" "${brew_binary}" reinstall --cask microfat
verify_installation reinstall
# With only the first public recipe, upgrade is a same-version check. The prior
# native qualification covers an actual fixture transition and checksum rollback.
run_recorded success upgrade.log "${brew_environment[@]}" "${brew_binary}" upgrade --cask microfat
verify_installation upgrade
run_recorded success uninstall.log "${brew_environment[@]}" "${brew_binary}" uninstall --cask microfat
for product in microfat microfat-stub microfat-stub-minimal; do
    [[ ! -e ${brew_prefix}/bin/${product} && ! -L ${brew_prefix}/bin/${product} ]] || fail 'uninstall left a product entrypoint'
done
[[ ! -e ${brew_prefix}/Caskroom/microfat/${release_version} && ! -L ${brew_prefix}/Caskroom/microfat/${release_version} ]] ||
    fail 'uninstall left the installed versioned cask payload'
[[ $(< "${unrelated}") == 'preserve unrelated binary' && $(< "${cache_sentinel}") == 'preserve unrelated cache' && \
    $(< "${workload_sentinel}") == 'preserve unrelated workload' ]] || fail 'uninstall changed unrelated data'
[[ -d ${qual_root}/payload-cache ]] || fail 'uninstall removed workload cache'
script_path=$(realpath -e -- "${BASH_SOURCE[0]}")
qualification_source=$(git -C "${script_path%/*}/.." rev-parse HEAD)
script_digest=$(digest_file "${script_path}")
jq -n --slurpfile verified "${verification}" --arg tag "${release_tag}" --arg arch "${native_arch}" \
    --arg tap_source "${tap_source}" --arg qualification_source "${qualification_source}" --arg script_sha256 "${script_digest}" \
    --arg recipe_sha256 "${actual_recipe}" --arg brew_source "${brew_source}" \
    '{schema: 1, result: "pass", tag: $tag, arch: $arch, source: $verified[0].source, release_id: $verified[0].release_id,
      checksums_sha256: $verified[0].checksums_sha256, qualification_source: $qualification_source,
      script_sha256: $script_sha256, tap_source: $tap_source, recipe_sha256: $recipe_sha256,
      brew_source: $brew_source, public_upgrade: "same-version", install: true, reinstall: true,
      uninstall: true, unrelated_data_preserved: true}' > "${evidence_root}/summary.json"
printf 'PASS native %s: public remote Homebrew install, authenticated products and lifecycle\n' "${native_arch}"
