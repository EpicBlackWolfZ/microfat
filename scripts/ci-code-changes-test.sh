#!/usr/bin/env bash
# Exercise real Git diffs, including renames, unusual filenames and missing history.
set -euo pipefail
IFS=$'\n\t'

script_dir=$(dirname "${BASH_SOURCE[0]}")
script_dir=$(cd "${script_dir}" && pwd)
classifier="${script_dir}/ci-code-changes.sh"
fixture_dir=$(mktemp -d)
trap 'rm -rf -- "${fixture_dir}"' EXIT

git init --quiet "${fixture_dir}"
cd "${fixture_dir}"
git config user.name 'CI classification test'
git config user.email 'ci-classification@example.invalid'
git config commit.gpgsign false
mkdir -p docs/nested src .github/workflows
printf 'documentation\n' > README.md
printf 'guide\n' > docs/guide.md
printf 'package main\n' > src/main.go
git add .
git commit --quiet -m 'test: baseline'
base_sha=$(git rev-parse HEAD)

assert_output() {
    local expected="$1" label="$2" base="$3" head="$4" actual
    actual=$(bash "${classifier}" "${base}" "${head}")
    if [[ "${actual}" != "${expected}" ]]; then
        printf 'FAIL: %s: expected %s, got %s\n' "${label}" "${expected}" "${actual}" >&2
        exit 1
    fi
    printf 'PASS: %s\n' "${label}"
}

commit_fixture() {
    git add -A
    git commit --quiet -m 'test: fixture'
}

assert_output true 'empty diff runs code checks' "${base_sha}" HEAD

for path in README.md docs/guide.md docs/nested/guide.md LICENSE; do
    printf 'updated documentation\n' >> "${path}"
    commit_fixture
    assert_output false "documentation: ${path}" HEAD^ HEAD
done

for path in src/main.go docs/example.go .github/workflows/ci.yml go.mod .gitignore \
    scripts.md.sh src/fixture.md $'src/line\nbreak.go'; do
    printf 'code or configuration\n' > "${path}"
    commit_fixture
    assert_output true "code/configuration: ${path}" HEAD^ HEAD
done

printf 'updated\n' >> README.md
printf 'changed\n' >> src/main.go
commit_fixture
assert_output true 'mixed docs and code' HEAD^ HEAD

git mv src/main.go docs/renamed.md
commit_fixture
assert_output true 'code renamed into docs' HEAD^ HEAD
git mv docs/renamed.md src/restored.go
commit_fixture
assert_output true 'docs renamed into code' HEAD^ HEAD
git rm --quiet src/restored.go
commit_fixture
assert_output true 'deleted code' HEAD^ HEAD
git rm --quiet docs/guide.md
commit_fixture
assert_output false 'deleted documentation' HEAD^ HEAD

for index in {1..1001}; do
    printf 'documentation\n' > "docs/page-${index}.md"
done
printf 'code\n' > z-last.go
commit_fixture
assert_output true 'code after 1001 documentation files' HEAD^ HEAD

# The target advanced independently: classify the actual merge result, not just
# the PR tip or the last authored commit. This also models checkout fetch-depth: 2.
git switch --quiet -c docs-branch "${base_sha}"
printf 'branch documentation\n' >> README.md
commit_fixture
git switch --quiet -c target-branch "${base_sha}"
printf 'target code\n' >> src/main.go
commit_fixture
git merge --quiet --no-ff docs-branch -m 'test: synthetic PR merge'
git clone --quiet --depth 2 "file://${fixture_dir}" shallow
(
    cd shallow
    assert_output false 'docs PR merge with independently advanced base, shallow checkout' HEAD^1 HEAD
)

status=0
bash "${classifier}" missing-ref HEAD > result.txt 2> error.txt || status=$?
if [[ "${status}" -eq 0 || -s result.txt ]]; then
    echo 'FAIL: unavailable history must fail without a skip decision' >&2
    exit 1
fi
echo 'PASS: unavailable history fails closed'

# Inject a Git diff failure after partial output to catch process-substitution
# patterns that would otherwise lose Git's nonzero exit status.
real_git=$(command -v git)
mkdir fake-bin
cat > fake-bin/git <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
IFS=$'\n\t'
if [[ "$1" == diff ]]; then
    printf 'docs/guide.md\0'
    exit 1
fi
exec "${REAL_GIT}" "$@"
EOF
chmod +x fake-bin/git
status=0
REAL_GIT="${real_git}" PATH="${fixture_dir}/fake-bin:${PATH}" \
    bash "${classifier}" HEAD^ HEAD > result.txt 2> error.txt || status=$?
if [[ "${status}" -eq 0 || -s result.txt ]]; then
    echo 'FAIL: partial Git failure must fail without a skip decision' >&2
    exit 1
fi
echo 'PASS: partial Git failure fails closed'
