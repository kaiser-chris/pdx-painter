#!/usr/bin/env bash
# Works out the version to release, from the release tags of the repository
# and which part of the version to increase: major, minor or patch.
#
# Prints GitHub Actions outputs: previous-tag (the tag of the latest release,
# empty when there is none), next (the version to release, such as 1.2.3) and
# tag (its tag, such as v1.2.3). The tags start with a v, which Go asks of a
# module's versions, so that go install ...@v1.2.3 finds them. The first
# release is 0.1.0 for a minor or patch increment.
set -euo pipefail

increment="${1:?usage: next-version.sh major|minor|patch}"

# The highest release tag counts, whichever commit it is on.
latest=$(git tag --list 'v*' |
	grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' |
	sort -V |
	tail -n 1 || true)

if [[ -z "$latest" ]]; then
	previous="0.0.0"
else
	previous="${latest#v}"
fi

IFS=. read -r major minor patch <<<"$previous"

case "$increment" in
major)
	major=$((major + 1))
	minor=0
	patch=0
	;;
minor)
	minor=$((minor + 1))
	patch=0
	;;
patch)
	# Nothing released yet has nothing to patch.
	if [[ -z "$latest" ]]; then
		minor=1
	else
		patch=$((patch + 1))
	fi
	;;
*)
	echo "unknown increment '$increment': use major, minor or patch" >&2
	exit 1
	;;
esac

next="$major.$minor.$patch"

if git rev-parse --verify --quiet "refs/tags/v$next" >/dev/null; then
	echo "the tag v$next exists already" >&2
	exit 1
fi

echo "previous-tag=$latest"
echo "next=$next"
echo "tag=v$next"
