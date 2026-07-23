#!/bin/sh
set -eu

mode=${1:-}
case "$mode" in
	compatibility|live) ;;
	*)
		echo "usage: $0 compatibility|live" >&2
		exit 2
		;;
esac

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
module_dir=$(CDPATH='' cd -- "$script_dir/.." && pwd)
if test -n "${JOEYDB_SOURCE:-}"; then
	source_dir=$JOEYDB_SOURCE
else
	git_common_dir=$(git -C "$module_dir" rev-parse --path-format=absolute --git-common-dir)
	repository_dir=$(dirname -- "$git_common_dir")
	source_dir="$repository_dir/../joeydb"
fi
reference_commit=223eacc01d3707eb37c9055fa99dc359f735eeb1

if test ! -d "$source_dir/.git"; then
	echo "JoeyDB source repository not found at $source_dir" >&2
	exit 1
fi
actual_commit=$(git -C "$source_dir" rev-parse HEAD)
if test "$actual_commit" != "$reference_commit"; then
	echo "JoeyDB source HEAD is $actual_commit; exact $reference_commit is required" >&2
	exit 1
fi
if ! git -C "$source_dir" diff --quiet ||
	! git -C "$source_dir" diff --cached --quiet; then
	echo "JoeyDB source has tracked changes; an exact $reference_commit tree is required" >&2
	exit 1
fi
if ! cmp -s "$source_dir/docs/schema/joeydb.ingestion.v1.schema.json" \
	"$module_dir/schema/joeydb.ingestion.v1.schema.json"; then
	echo "published ingestion schema differs from JoeyDB $reference_commit" >&2
	exit 1
fi

temporary_dir=$(mktemp -d "${TMPDIR:-/tmp}/joeydb-go-reference.XXXXXX")
cleanup() {
	case "$temporary_dir" in
		"${TMPDIR:-/tmp}"/joeydb-go-reference.*)
			rm -rf -- "$temporary_dir"
			;;
		*)
			echo "refusing to clean unexpected temporary path: $temporary_dir" >&2
			;;
	esac
}
trap cleanup EXIT HUP INT TERM

(
	cd "$source_dir"
	go build -o "$temporary_dir/joey" ./cmd/joey
)

if test "$mode" = compatibility; then
	(
		cd "$module_dir"
		JOEYDB_REFERENCE_CLI="$temporary_dir/joey" \
			go test ./ingest -run TestReferenceCLICompatibility -count=1 -v
	)
	exit 0
fi

(
	cd "$source_dir"
	go build -o "$temporary_dir/joeydbd" ./cmd/joeydbd
)
(
	cd "$module_dir"
	JOEYDB_REFERENCE_CLI="$temporary_dir/joey" \
	JOEYDBD_REFERENCE_BINARY="$temporary_dir/joeydbd" \
		go test . -run '^TestLive' -count=1 -v
)
