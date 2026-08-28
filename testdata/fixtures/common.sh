# Shared determinism pins. Source this before touching git in any fixture script.
# Without these, SHAs move on every run and every golden file churns.
set -euo pipefail

export GIT_CONFIG_GLOBAL=/dev/null
export GIT_CONFIG_SYSTEM=/dev/null
export GIT_AUTHOR_NAME=fixture
export GIT_AUTHOR_EMAIL=fixture@example.com
export GIT_COMMITTER_NAME=fixture
export GIT_COMMITTER_EMAIL=fixture@example.com
export GIT_AUTHOR_DATE="2020-01-01T00:00:00+0000"
export GIT_COMMITTER_DATE="2020-01-01T00:00:00+0000"
export TZ=UTC
export LC_ALL=C

git_init() {
	git init -q -b main "$1"
	git -C "$1" config core.quotepath true   # on purpose: the default, and what breaks naive parsers
}
