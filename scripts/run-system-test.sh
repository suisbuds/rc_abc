#!/bin/sh

set -eu

mode=${1:-}

case "$mode" in
single)
	project=${RC_SINGLE_TEST_PROJECT:-rc_abc_single_test}
	export RC_HTTP_PORT=${RC_SINGLE_TEST_HTTP_PORT:-8080}
	export RC_POSTGRES_PORT=${RC_SINGLE_TEST_POSTGRES_PORT:-15432}
	export RC_RECEIVER_PORT=${RC_SINGLE_TEST_RECEIVER_PORT:-18081}
	export RC_WORKER_CONCURRENCY=${RC_SINGLE_TEST_WORKER_CONCURRENCY:-4}
	export RC_MAX_ATTEMPTS=3
	export RC_BASE_BACKOFF=200ms
	export RC_MAX_BACKOFF=1s
	export MOCK_FAILURES_BEFORE_SUCCESS=2
	supplier_url=${SUPPLIER_URL:-http://mockreceiver:8081/events}
	if [ -n "${SUPPLIER_EXPECTED_ATTEMPTS:-}" ]; then
		expected_attempts=$SUPPLIER_EXPECTED_ATTEMPTS
	elif [ "$supplier_url" = "http://mockreceiver:8081/events" ]; then
		expected_attempts=3
	else
		expected_attempts=1
	fi
	;;
all)
	project=${RC_ALL_TEST_PROJECT:-rc_abc_all_test}
	export RC_HTTP_PORT=${RC_ALL_TEST_HTTP_PORT:-18080}
	export RC_POSTGRES_PORT=${RC_ALL_TEST_POSTGRES_PORT:-25432}
	export RC_RECEIVER_PORT=${RC_ALL_TEST_RECEIVER_PORT:-28081}
	export RC_WORKER_CONCURRENCY=${RC_ALL_TEST_WORKER_CONCURRENCY:-16}
	export RC_MAX_ATTEMPTS=3
	export RC_BASE_BACKOFF=100ms
	export RC_MAX_BACKOFF=500ms
	export MOCK_FAILURES_BEFORE_SUCCESS=0
	supplier_url=http://mockreceiver:8081/events
	expected_attempts=1
	;;
*)
	echo "usage: $0 <single|all>" >&2
	exit 2
	;;
esac

echo "Starting ${mode} stack project=${project} api=http://localhost:${RC_HTTP_PORT}"
docker compose -p "$project" --profile demo up -d --build --wait --force-recreate

if ! go run ./tests/system -mode "$mode" -api-url "http://localhost:${RC_HTTP_PORT}" \
	-supplier-url "$supplier_url" -expected-attempts "$expected_attempts"; then
	echo "${mode} test failed; service logs follow" >&2
	docker compose -p "$project" --profile demo logs --no-color rc mockreceiver >&2
	exit 1
fi

if [ "$mode" = "single" ] && [ "$supplier_url" = "http://mockreceiver:8081/events" ]; then
	echo "Local receiver delivery evidence:"
	docker compose -p "$project" --profile demo logs --no-color mockreceiver
	echo "Single-test stack is still running. Stop it with: make single-test-down"
else
	if [ "$mode" = "single" ]; then
		echo "External supplier test completed against: $supplier_url"
		echo "Single-test stack is still running. Stop it with: make single-test-down"
	else
		echo "All-test stack is still running. Stop it with: make all-test-down"
	fi
fi
