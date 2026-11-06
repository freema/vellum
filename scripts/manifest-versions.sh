#!/usr/bin/env bash
# The release version carried by the distribution manifests: server.json
# (MCP registry), the Claude Desktop bundle manifest and the Claude Code
# plugin marketplace. Nothing derives them from the tag, so they are bumped
# by hand, and 1.9.0 once survived three releases that way. This script is
# the one list of those fields: `task bump` rewrites them and the release
# workflow checks them.
#
#   scripts/manifest-versions.sh bump 1.13.0    set every field (and README image tag) to 1.13.0
#   scripts/manifest-versions.sh check 1.13.0   fail, naming each file that differs
#   scripts/manifest-versions.sh list           print file, field and version
#
# Needs bash and jq. Paths are relative to the repository root.
set -euo pipefail

cd "$(dirname "$0")/.."

# file|jq filter selecting every version field of that file. A field that
# is added later (another package in server.json) is caught by bump, which
# refuses to run while a file has more "version" keys than its filter selects.
MANIFESTS=(
	'server.json|.version, (.packages[] | select(has("version")) | .version)'
	'mcpb/manifest.json|.version'
	'.claude-plugin/marketplace.json|.plugins[].version'
	'plugins/claude/vellum/.claude-plugin/plugin.json|.version'
)

# file|jq filter selecting identifiers that pin a release: an image tag
# (ghcr.io/freema/vellum:1.2.3) or a release download URL
# (.../releases/download/v1.2.3/vellum.mcpb). The MCP registry takes the
# version from there, not from a "version" field.
PINNED=(
	'server.json|.packages[].identifier'
)

# Docs whose examples pin the image (ghcr.io/freema/vellum:1.2.3). bump
# rewrites those tags too, so the examples never lag behind a release; the
# Go tests check that they match server.json. They are not part of check:
# a stale example must not stop a release.
DOCS=(
	'README.md'
)

die() {
	echo "manifest-versions: $*" >&2
	exit 1
}

command -v jq >/dev/null || die "jq is required (https://jqlang.org)"

# fields FILE FILTER prints "<path>\t<value>" for every selected field,
# e.g. ".packages[0].version<TAB>1.9.0".
fields() {
	jq -r "path($2) as \$p
		| [(\$p | map(if type == \"number\" then \"[\(.)]\" else \".\(.)\" end) | join(\"\")),
		   (getpath(\$p) // \"(missing)\" | tostring)]
		| @tsv" "$1"
}

# pinned FILE FILTER prints "<path>\t<version>" for every selected
# identifier, the version being "(unpinned)" when the identifier has none.
pinned() {
	local path id
	while IFS=$'\t' read -r path id; do
		if [[ $id =~ (:|/releases/download/v)([0-9]+\.[0-9]+\.[0-9]+)(/|$) ]]; then
			printf '%s\t%s\n' "$path" "${BASH_REMATCH[2]}"
		else
			printf '%s\t(unpinned)\n' "$path"
		fi
	done < <(fields "$1" "$2")
}

# every_field prints "<file>\t<path>\t<version>" for each version field
# and each pinned identifier.
every_field() {
	local entry file filter
	for entry in "${MANIFESTS[@]}"; do
		file=${entry%%|*} filter=${entry#*|}
		[[ -f $file ]] || die "$file is missing"
		fields "$file" "$filter" | sed "s|^|$file	|"
	done
	for entry in "${PINNED[@]}"; do
		file=${entry%%|*} filter=${entry#*|}
		[[ -f $file ]] || die "$file is missing"
		pinned "$file" "$filter" | sed "s|^|$file	|"
	done
}

cmd_list() {
	every_field
}

cmd_check() {
	local want=$1 file path got bad=0 listing
	# Collected first: a missing file must stop the check, not end a loop.
	listing=$(every_field)
	while IFS=$'\t' read -r file path got; do
		if [[ $got != "$want" ]]; then
			bad=1
			echo "$file is out of date: $path is $got, the tag needs $want" >&2
			if [[ ${GITHUB_ACTIONS:-} == true ]]; then
				echo "::error file=$file::$path is $got, the tag needs $want"
			fi
		fi
	done <<<"$listing"
	if ((bad)); then
		echo "Run 'task bump VERSION=$want', commit, and tag that commit." >&2
		exit 1
	fi
	echo "Manifest versions are $want."
}

cmd_bump() {
	local new=${1#v} entry file filter selected keys
	[[ $new =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] ||
		die "version must be x.y.z, got '$1' (usage: task bump VERSION=x.y.z; pre-releases need no bump)"

	# Validate every file before touching any, so a refusal changes nothing.
	for entry in "${MANIFESTS[@]}"; do
		file=${entry%%|*} filter=${entry#*|}
		[[ -f $file ]] || die "$file is missing"
		selected=$(jq "[path($filter)] | length" "$file")
		keys=$(grep -oE '"version"[[:space:]]*:' "$file" | wc -l | tr -d ' ')
		[[ $selected == "$keys" ]] ||
			die "$file has $keys \"version\" keys but the list in $0 selects $selected; update MANIFESTS first"
	done
	for entry in "${PINNED[@]}"; do
		file=${entry%%|*} filter=${entry#*|}
		[[ -f $file ]] || die "$file is missing"
		if [[ $(pinned "$file" "$filter") == *"(unpinned)"* ]]; then
			die "$file has an identifier without a release version; pin it or update PINNED in $0 first"
		fi
	done

	for file in "${DOCS[@]}"; do
		[[ -f $file ]] || die "$file is missing"
	done

	# Global, not local: the EXIT trap runs after this function returned.
	tmp=$(mktemp)
	trap 'rm -f "$tmp"' EXIT
	for entry in "${MANIFESTS[@]}"; do
		file=${entry%%|*}
		# The version fields, then the version inside pinned identifiers.
		sed -E -e 's/("version"[[:space:]]*:[[:space:]]*")[^"]*"/\1'"$new"'"/g' \
			-e 's#("identifier"[[:space:]]*:[[:space:]]*"[^"]*(:|/releases/download/v))[0-9]+\.[0-9]+\.[0-9]+#\1'"$new"'#g' \
			"$file" >"$tmp"
		# cat, not mv: keeps the file's mode and any hard links.
		cat "$tmp" >"$file"
	done
	for file in "${DOCS[@]}"; do
		sed -E 's#(ghcr\.io/freema/vellum:)[0-9]+\.[0-9]+\.[0-9]+#\1'"$new"'#g' "$file" >"$tmp"
		cat "$tmp" >"$file"
	done
	cmd_check "$new"
}

case "${1:-}" in
list) cmd_list ;;
check)
	[[ $# -eq 2 ]] || die "usage: $0 check x.y.z"
	cmd_check "${2#v}"
	;;
bump)
	[[ $# -eq 2 ]] || die "usage: $0 bump x.y.z"
	cmd_bump "$2"
	;;
*) die "usage: $0 list | check x.y.z | bump x.y.z" ;;
esac
