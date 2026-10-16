#!/usr/bin/env bash
# Smoke test for the always-https image.
#
# Usage:  bash scripts/smoke.sh            (build first with: make docker-build)
#         IMAGE=docker.sunet.se/always-https:v1.2.3 bash scripts/smoke.sh
#         KEEP=1 bash scripts/smoke.sh     (leave containers running for poking)
#
# What it does on the Docker daemon:
#   - pulls $CURL_IMAGE if it is not present
#   - creates the network "ah-smoke"
#   - starts 3 containers from $IMAGE (no ports are published on the host):
#       ah-smoke-acme  plain instance, used as ACME_URL backend
#       ah-smoke-a     mode A: HTTP redirect only, ACME_URL -> ah-smoke-acme
#       ah-smoke-b     mode B: URLS set, TLS on :443, ACME_URL -> ah-smoke-acme
#   - runs each check in a throwaway $CURL_IMAGE container (--rm) on that network
#   - removes the containers and the network on exit (unless KEEP=1)
#
# Not covered here (see certs_test.go): mounted real certificates and chains.

set -euo pipefail

IMAGE=${IMAGE:-docker.sunet.se/always-https:latest}
CURL_IMAGE=${CURL_IMAGE:-curlimages/curl:latest}
NET=ah-smoke
ACME=ah-smoke-acme
A=ah-smoke-a
B=ah-smoke-b

pass=0
fail=0

cleanup() {
	docker rm -f "$ACME" "$A" "$B" >/dev/null 2>&1 || true
	docker network rm "$NET" >/dev/null 2>&1 || true
}

on_exit() {
	if ((fail > 0)); then
		for c in "$A" "$B" "$ACME"; do
			echo "----- logs $c"
			docker logs "$c" 2>&1 | tail -20 || true
		done
	fi
	if [[ ${KEEP:-} == 1 ]]; then
		echo "KEEP=1: containers left running; remove with: docker rm -f $ACME $A $B; docker network rm $NET"
	else
		cleanup
	fi
}

check() { # name want got
	if [[ $3 == "$2" ]]; then
		printf '  ok    %s\n' "$1"
		pass=$((pass + 1))
	else
		printf '  FAIL  %s\n        want: %q\n        got:  %q\n' "$1" "$2" "$3"
		fail=$((fail + 1))
	fi
}

# curl inside the test network.
c() { docker run --rm --network "$NET" "$CURL_IMAGE" -sS --max-time 5 "$@"; }

# "<status> <Location>" (Location empty if absent), trailing space trimmed.
resp() {
	local out
	out=$(c -o /dev/null -w '%{http_code} %header{location}' "$@" 2>&1) || true
	echo "${out% }"
}

# contains HAYSTACK NEEDLE: fixed-string match on captured output. Do not pipe
# straight into grep -q: with pipefail, grep exiting early SIGPIPEs the writer
# and the pipeline reports failure even though the line matched.
contains() { grep -qF -- "$2" <<<"$1"; }

wait_healthy() {
	local s=""
	for _ in $(seq 1 30); do
		s=$(docker inspect -f '{{.State.Health.Status}}' "$1" 2>/dev/null || echo missing)
		if [[ $s == healthy ]]; then
			break
		fi
		sleep 1
	done
	echo "$s"
}

run() { # name, extra docker run args...
	local name=$1
	shift
	docker run -d --name "$name" --network "$NET" \
		--health-interval 2s --health-start-period 1s \
		"$@" "$IMAGE" >/dev/null
}

# ---- setup -------------------------------------------------------------------

if ! docker image inspect "$IMAGE" >/dev/null 2>&1; then
	echo "image $IMAGE not found, run: make docker-build" >&2
	exit 1
fi

cleanup # leftovers from an aborted run
trap on_exit EXIT

docker image inspect "$CURL_IMAGE" >/dev/null 2>&1 || docker pull -q "$CURL_IMAGE" >/dev/null
docker network create "$NET" >/dev/null

run "$ACME"
run "$A" -e "ACME_URL=http://$ACME"
run "$B" --hostname web1.example \
	-e "ACME_URL=http://$ACME" \
	-e 'URLS=/a%https://example.org/b? /old/(.*)%https://example.org/new/$1?'

echo "image: $IMAGE ($(docker image inspect -f '{{.Size}}' "$IMAGE" | awk '{printf "%.1f MB", $1/1e6}'))"

# ---- image -------------------------------------------------------------------

echo "image"
check "runs as non-root" "65532:65532" "$(docker image inspect -f '{{.Config.User}}' "$IMAGE")"
if docker run --rm --entrypoint /bin/sh "$IMAGE" -c true >/dev/null 2>&1; then
	check "no shell in image (scratch)" "no shell" "has shell"
else
	check "no shell in image (scratch)" "no shell" "no shell"
fi
check "acme container healthy" "healthy" "$(wait_healthy "$ACME")"
check "A healthy" "healthy" "$(wait_healthy "$A")"
check "B healthy" "healthy" "$(wait_healthy "$B")"

# ---- mode A ------------------------------------------------------------------

echo "mode A (http redirect)"
check "redirect keeps path+query" "301 https://$A/foo?x=1" "$(resp "http://$A/foo?x=1")"
check "redirect POST" "301 https://$A/form" "$(resp -X POST "http://$A/form")"
check "redirect keeps Host port" "301 https://example.org:8080/p" "$(resp -H 'Host: example.org:8080' "http://$A/p")"
check "invalid Host rejected" "400" "$(resp -H 'Host: evil.com;x' "http://$A/")"
check "_lvs.txt" "ok" "$(c "http://$A/_lvs.txt" 2>&1)"
check "other .well-known redirected" "301 https://$A/.well-known/security.txt" "$(resp "http://$A/.well-known/security.txt")"

# The backend has no ACME_URL itself, so it answers 404; the access log proves
# the request was proxied with the path intact.
check "acme proxied (status from backend)" "404" "$(resp "http://$A/.well-known/acme-challenge/smoke-token-a")"
if contains "$(docker logs "$ACME" 2>&1)" 'uri=/.well-known/acme-challenge/smoke-token-a'; then
	check "acme request reached backend" "yes" "yes"
else
	check "acme request reached backend" "yes" "no"
fi

# ---- mode B ------------------------------------------------------------------

echo "mode B (URLS + TLS)"
check "http still redirects" "301 https://$B/x" "$(resp "http://$B/x")"
check "http _lvs.txt (Apache gave 404 here)" "ok" "$(c "http://$B/_lvs.txt" 2>&1)"
check "https _lvs.txt" "ok" "$(c -k "https://$B/_lvs.txt" 2>&1)"
check "rule with query" "301 https://example.org/b?q=1" "$(resp -k "https://$B/a?q=1")"
check "rule without query" "301 https://example.org/b" "$(resp -k "https://$B/a")"
check "rule with capture group" "301 https://example.org/new/x/y?z=2" "$(resp -k "https://$B/old/x/y?z=2")"
check "no partial match" "404" "$(resp -k "https://$B/a/b")"
check "unmatched path" "404" "$(resp -k "https://$B/zzz")"

if contains "$(c -kv -o /dev/null "https://$B/_lvs.txt" 2>&1)" 'subject: CN=web1.example'; then
	check "self-signed cert for HOSTNAME" "yes" "yes"
else
	check "self-signed cert for HOSTNAME" "yes" "no"
fi
if c -k --tls-max 1.1 "https://$B/_lvs.txt" >/dev/null 2>&1; then
	check "TLS <= 1.1 rejected" "rejected" "accepted"
else
	check "TLS <= 1.1 rejected" "rejected" "rejected"
fi

check "acme proxied over https" "404" "$(resp -k "https://$B/.well-known/acme-challenge/smoke-token-b")"
if contains "$(docker logs "$ACME" 2>&1)" 'uri=/.well-known/acme-challenge/smoke-token-b'; then
	check "https acme request reached backend" "yes" "yes"
else
	check "https acme request reached backend" "yes" "no"
fi

# ---- result ------------------------------------------------------------------

echo
echo "passed: $pass  failed: $fail"
((fail == 0))
