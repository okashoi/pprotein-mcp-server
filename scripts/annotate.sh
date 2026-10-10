#!/bin/sh
# Record a measure for a benchmark run as a pprotein memo, the same way the
# annotate_group MCP tool does, for use from a bench wrapper or from CI.
set -eu

PPROTEIN_URL="${PPROTEIN_URL:-http://localhost:9000}"

group=latest
score=null
pass=null
tags='[]'
comment=''

usage() {
	cat >&2 <<'EOF'
usage: annotate.sh [--group GROUP_ID] [--score NUMBER] [--pass true|false]
                   [--tags TAG,TAG] COMMENT

environment:
  PPROTEIN_URL  pprotein の URL (デフォルト: http://localhost:9000)
EOF
}

while [ $# -gt 0 ]; do
	case "$1" in
	--group) group=$2; shift 2 ;;
	--score) score=$2; shift 2 ;;
	--pass) pass=$2; shift 2 ;;
	--tags) tags=$(printf '%s' "$2" | jq -R 'split(",")'); shift 2 ;;
	-h | --help) usage; exit 0 ;;
	-*) echo "unknown option: $1" >&2; usage; exit 1 ;;
	*) comment=$1; shift ;;
	esac
done

if [ -z "$comment" ]; then
	usage
	exit 1
fi

# The most recent run is the one whose oldest snapshot is the newest, which is how the
# group resources order runs.
if [ "$group" = latest ]; then
	group=$(
		for type in pprof httplog slowlog memo; do
			curl -fsS "$PPROTEIN_URL/api/$type"
		done | jq -s -r 'add
			| map(select(.Snapshot.GroupId != ""))
			| group_by(.Snapshot.GroupId)
			| map({id: .[0].Snapshot.GroupId, at: (map(.Snapshot.Datetime) | min)})
			| max_by(.at).id // empty'
	)

	if [ -z "$group" ]; then
		echo "no benchmark run found on $PPROTEIN_URL" >&2
		exit 1
	fi
fi

text=$(jq -nc \
	--arg comment "$comment" \
	--argjson score "$score" \
	--argjson pass "$pass" \
	--argjson tags "$tags" \
	'{schema: "pprotein-measure/1", comment: $comment}
		+ (if $score == null then {} else {score: $score} end)
		+ (if $pass == null then {} else {pass: $pass} end)
		+ (if ($tags | length) == 0 then {} else {tags: $tags} end)')

curl -fsS -XPOST "$PPROTEIN_URL/api/memo" \
	-H 'Content-Type: application/json' \
	-d "$(jq -nc --arg group "$group" --arg text "$text" '{GroupId: $group, Label: "measure", Text: $text}')"

echo "annotated $group: $text"
