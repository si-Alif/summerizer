#!/usr/bin/env bash

set -E -u -o pipefail

SCRIPT_NAME="$(basename "$0")"

require_cmd() {
	if ! command -v "$1" >/dev/null 2>&1; then
		echo "error: required command not found: $1" >&2
		exit 1
	fi
}

log() {
	printf '%s %s\n' "$(date +'%Y-%m-%dT%H:%M:%S%z')" "$*"
}

API_BASE_URL="${API_BASE_URL:-http://localhost:4000}"
POLL_INTERVAL_SEC="${POLL_INTERVAL_SEC:-5}"
WAIT_TIMEOUT_SEC="${WAIT_TIMEOUT_SEC:-600}"
MIN_COMPLETED_FOR_QUERY="${MIN_COMPLETED_FOR_QUERY:-1}"
CURL_TIMEOUT_SEC="${CURL_TIMEOUT_SEC:-90}"
SEARCH_TIMEOUT_SEC="${SEARCH_TIMEOUT_SEC:-90}"
ASK_TIMEOUT_SEC="${ASK_TIMEOUT_SEC:-180}"
ASK_RETRY_ON_TIMEOUT="${ASK_RETRY_ON_TIMEOUT:-1}"
ASK_RETRY_TIMEOUT_SEC="${ASK_RETRY_TIMEOUT_SEC:-300}"
SOURCE_ADD_INTERVAL_SEC="${SOURCE_ADD_INTERVAL_SEC:-0.8}"
SOURCE_ADD_RETRY_MAX="${SOURCE_ADD_RETRY_MAX:-3}"
SOURCE_ADD_RETRY_DELAY_SEC="${SOURCE_ADD_RETRY_DELAY_SEC:-1}"
SEARCH_TOP_K="${SEARCH_TOP_K:-5}"
ASK_TOP_K="${ASK_TOP_K:-5}"
SEARCH_QUERY="${SEARCH_QUERY:-What is retrieval augmented generation?}"
ASK_QUESTION="${ASK_QUESTION:-Summarize this collection in 3 concise points and cite source URLs.}"

RUN_ID="${RUN_ID:-$(date +%Y%m%d-%H%M%S)}"
OUT_ROOT="${OUT_ROOT:-./tmp/phase2/e2e}"
RUN_DIR="${OUT_ROOT}/${RUN_ID}"
LOG_FILE="${RUN_DIR}/runner.log"
POLL_FILE="${RUN_DIR}/poll_status.csv"
SUMMARY_FILE="${RUN_DIR}/summary.json"
SOURCE_RESULT_FILE="${RUN_DIR}/source_create_results.tsv"

USER_EMAIL="${USER_EMAIL:-e2e.${RUN_ID}@summerizer.local}"
USER_PASSWORD="${USER_PASSWORD:-P@ssword12345!}"
COLLECTION_TITLE="${COLLECTION_TITLE:-phase2-e2e-${RUN_ID}}"
SOURCE_URLS_FILE="${SOURCE_URLS_FILE:-}"
AUTH_TOKEN="${AUTH_TOKEN:-}"

if [[ -z "$AUTH_TOKEN" ]] && [[ -z "${SUMMERIZER_DB_DSN:-}" ]]; then
	echo "error: SUMMERIZER_DB_DSN is required when AUTH_TOKEN is not provided." >&2
	exit 1
fi

mkdir -p "$RUN_DIR"
exec > >(tee -a "$LOG_FILE") 2>&1

on_exit() {
	local code="$?"
	log "script exiting: code=${code}, run_dir=${RUN_DIR}"
}

trap on_exit EXIT

require_cmd curl
require_cmd jq
if [[ -z "$AUTH_TOKEN" ]]; then
	require_cmd psql
fi

DEFAULT_SOURCE_URLS=(
	"https://en.wikipedia.org/wiki/Retrieval-augmented_generation"
	"https://en.wikipedia.org/wiki/Vector_database"
	"https://www.postgresql.org/docs/current/indexes-intro.html"
	"https://go.dev/blog/concurrency-is-not-parallelism"
	"https://en.wikipedia.org/wiki/Go_(programming_language)"
)

SOURCE_URLS=()
if [[ -n "$SOURCE_URLS_FILE" ]]; then
	if [[ ! -f "$SOURCE_URLS_FILE" ]]; then
		log "error: SOURCE_URLS_FILE does not exist: $SOURCE_URLS_FILE"
		exit 1
	fi

	while IFS= read -r line; do
		if [[ -z "$line" ]] || [[ "$line" =~ ^[[:space:]]*# ]]; then
			continue
		fi
		SOURCE_URLS+=("$line")
	done < "$SOURCE_URLS_FILE"
else
	SOURCE_URLS=("${DEFAULT_SOURCE_URLS[@]}")
fi

if [[ "${INCLUDE_FAILURE_PROBE:-0}" == "1" ]]; then
	SOURCE_URLS+=("https://www.cloudflare.com/learning/cdn/what-is-a-cdn/")
fi

if [[ "${#SOURCE_URLS[@]}" -eq 0 ]]; then
	log "error: no source URLs configured"
	exit 1
fi

api_call() {
	local method="$1"
	local path="$2"
	local body="$3"
	local out_file="$4"
	local requires_auth="$5"
	local timeout_sec="${6:-$CURL_TIMEOUT_SEC}"

	local -a headers=("-H" "Content-Type: application/json")
	if [[ "$requires_auth" == "1" ]]; then
		headers+=("-H" "Authorization: Bearer ${AUTH_TOKEN}")
	fi

	local -a data_args=()
	if [[ -n "$body" ]]; then
		data_args=("--data" "$body")
	fi

	local status
	status="$(curl -sS --max-time "$timeout_sec" -o "$out_file" -w "%{http_code}" -X "$method" "${headers[@]}" "${data_args[@]}" "${API_BASE_URL}${path}")"
	echo "$status"
}

should_retry_source_add() {
	case "$1" in
		429 | 500 | 502 | 503 | 504 | 000)
			return 0
			;;
		*)
			return 1
			;;
	esac
}

run_search_and_ask() {
	local collection_id="$1"

	local search_payload ask_payload
	search_payload="$(jq -nc --arg q "$SEARCH_QUERY" --argjson k "$SEARCH_TOP_K" '{query: $q, top_k: $k}')"
	ask_payload="$(jq -nc --arg q "$ASK_QUESTION" --argjson k "$ASK_TOP_K" '{question: $q, top_k: $k}')"

	SEARCH_HTTP_STATUS="$(api_call "POST" "/v1/collections/${collection_id}/search" "$search_payload" "${RUN_DIR}/search_response.json" 1 "$SEARCH_TIMEOUT_SEC")"
	if [[ "$SEARCH_HTTP_STATUS" == "200" ]]; then
		SEARCH_RESULT_COUNT="$(jq '[.results[]?] | length' "${RUN_DIR}/search_response.json" 2>/dev/null || echo "0")"
		log "search request succeeded: status=${SEARCH_HTTP_STATUS}, results=${SEARCH_RESULT_COUNT}"
	else
		SEARCH_RESULT_COUNT=0
		log "search request failed: status=${SEARCH_HTTP_STATUS}"
	fi

	ASK_HTTP_STATUS="$(api_call "POST" "/v1/collections/${collection_id}/ask" "$ask_payload" "${RUN_DIR}/ask_response.json" 1 "$ASK_TIMEOUT_SEC")"
	if [[ "$ASK_HTTP_STATUS" == "000" ]] && [[ "$ASK_RETRY_ON_TIMEOUT" == "1" ]]; then
		log "ask request timed out; retrying with timeout=${ASK_RETRY_TIMEOUT_SEC}s"
		ASK_HTTP_STATUS="$(api_call "POST" "/v1/collections/${collection_id}/ask" "$ask_payload" "${RUN_DIR}/ask_response.json" 1 "$ASK_RETRY_TIMEOUT_SEC")"
	fi
	if [[ "$ASK_HTTP_STATUS" == "200" ]]; then
		ASK_SOURCE_COUNT="$(jq '[.sources[]?] | length' "${RUN_DIR}/ask_response.json" 2>/dev/null || echo "0")"
		ASK_ANSWER_LENGTH="$(jq '.answer | length' "${RUN_DIR}/ask_response.json" 2>/dev/null || echo "0")"
		log "ask request succeeded: status=${ASK_HTTP_STATUS}, sources=${ASK_SOURCE_COUNT}, answer_chars=${ASK_ANSWER_LENGTH}"
	else
		ASK_SOURCE_COUNT=0
		ASK_ANSWER_LENGTH=0
		log "ask request failed: status=${ASK_HTTP_STATUS}"
	fi
}

log "starting ${SCRIPT_NAME}"
log "run_id=${RUN_ID}"
log "api_base_url=${API_BASE_URL}"
log "sources_to_add=${#SOURCE_URLS[@]}"

HEALTH_STATUS="$(api_call "GET" "/v1/healthcheck" "" "${RUN_DIR}/healthcheck.json" 0)"
if [[ "$HEALTH_STATUS" != "200" ]]; then
	log "error: healthcheck failed with status=${HEALTH_STATUS}"
	exit 1
fi
log "healthcheck passed"

if [[ -z "$AUTH_TOKEN" ]]; then
	REGISTER_PAYLOAD="$(jq -nc --arg email "$USER_EMAIL" --arg password "$USER_PASSWORD" --arg fullname "Phase2 E2E User" '{email:$email, password:$password, fullname:$fullname}')"
	REGISTER_STATUS="$(api_call "POST" "/v1/users" "$REGISTER_PAYLOAD" "${RUN_DIR}/register_user.json" 0)"
	if [[ "$REGISTER_STATUS" != "201" ]]; then
		log "error: user registration failed with status=${REGISTER_STATUS}"
		log "response saved at ${RUN_DIR}/register_user.json"
		exit 1
	fi

	USER_ID="$(jq -r '.user.id // empty' "${RUN_DIR}/register_user.json")"
	if [[ -z "$USER_ID" ]]; then
		log "error: could not parse user id from registration response"
		exit 1
	fi
	log "registered user id=${USER_ID}, email=${USER_EMAIL}"

	# Use explicit SQL literal escaping instead of psql variable interpolation,
	# which can fail in some environments when passed via -c.
	SAFE_USER_EMAIL="${USER_EMAIL//\'/\'\'}"
	ACTIVATED_USER_ID="$(psql "${SUMMERIZER_DB_DSN}" -Atqv ON_ERROR_STOP=1 -c "UPDATE users SET activated = true WHERE email = '${SAFE_USER_EMAIL}' RETURNING id;")"
	ACTIVATED_USER_ID="$(echo "$ACTIVATED_USER_ID" | tr -d '[:space:]')"
	if [[ -z "$ACTIVATED_USER_ID" ]]; then
		log "error: failed to activate user via database update"
		exit 1
	fi
	log "user activated via db: user_id=${ACTIVATED_USER_ID}"

	TOKEN_PAYLOAD="$(jq -nc --arg email "$USER_EMAIL" --arg password "$USER_PASSWORD" '{email:$email, password:$password}')"
	TOKEN_STATUS="$(api_call "POST" "/v1/tokens/authentication" "$TOKEN_PAYLOAD" "${RUN_DIR}/auth_token.json" 0)"
	if [[ "$TOKEN_STATUS" != "201" ]]; then
		log "error: authentication token creation failed with status=${TOKEN_STATUS}"
		exit 1
	fi

	AUTH_TOKEN="$(jq -r '.authentication_token.token // empty' "${RUN_DIR}/auth_token.json")"
	if [[ -z "$AUTH_TOKEN" ]]; then
		log "error: failed to parse authentication token"
		exit 1
	fi
	log "authentication token acquired"
else
	log "using provided AUTH_TOKEN (registration/activation skipped)"
fi

COLLECTION_PAYLOAD="$(jq -nc --arg title "$COLLECTION_TITLE" --arg description "Phase2 e2e scripted run ${RUN_ID}" '{title:$title, description:$description}')"
COLLECTION_STATUS="$(api_call "POST" "/v1/collections" "$COLLECTION_PAYLOAD" "${RUN_DIR}/create_collection.json" 1)"
if [[ "$COLLECTION_STATUS" != "201" ]]; then
	log "error: collection creation failed with status=${COLLECTION_STATUS}"
	exit 1
fi

COLLECTION_ID="$(jq -r '.collection.id // empty' "${RUN_DIR}/create_collection.json")"
if [[ -z "$COLLECTION_ID" ]]; then
	log "error: failed to parse collection id"
	exit 1
fi
log "collection created: collection_id=${COLLECTION_ID}"

printf 'index\thttp_status\turl\tresponse_file\n' > "$SOURCE_RESULT_FILE"

SOURCE_CREATE_SUCCESS=0
SOURCE_CREATE_FAILURE=0

for i in "${!SOURCE_URLS[@]}"; do
	idx=$((i + 1))
	url="${SOURCE_URLS[$i]}"
	title="source-${idx}"
	resp_file="${RUN_DIR}/source_create_${idx}.json"
	attempt=1
	max_attempts=$((SOURCE_ADD_RETRY_MAX + 1))

	payload="$(jq -nc --arg url "$url" --arg title "$title" '{url:$url, title:$title}')"
	while true; do
		http_status="$(api_call "POST" "/v1/collections/${COLLECTION_ID}/sources" "$payload" "$resp_file" 1)"
		if [[ "$http_status" == "201" ]]; then
			break
		fi

		if should_retry_source_add "$http_status" && (( attempt < max_attempts )); then
			log "source add temporary failure: idx=${idx} attempt=${attempt}/${max_attempts} status=${http_status}; retrying in ${SOURCE_ADD_RETRY_DELAY_SEC}s"
			sleep "$SOURCE_ADD_RETRY_DELAY_SEC"
			attempt=$((attempt + 1))
			continue
		fi

		break
	done

	printf '%s\t%s\t%s\t%s\n' "$idx" "$http_status" "$url" "$resp_file" >> "$SOURCE_RESULT_FILE"

	if [[ "$http_status" == "201" ]]; then
		SOURCE_CREATE_SUCCESS=$((SOURCE_CREATE_SUCCESS + 1))
		log "source added: idx=${idx} status=${http_status} url=${url}"
	else
		SOURCE_CREATE_FAILURE=$((SOURCE_CREATE_FAILURE + 1))
		log "source add failed (continuing): idx=${idx} status=${http_status} url=${url}"
	fi

	if [[ "$SOURCE_ADD_INTERVAL_SEC" != "0" ]]; then
		sleep "$SOURCE_ADD_INTERVAL_SEC"
	fi
done

if [[ "$SOURCE_CREATE_SUCCESS" -eq 0 ]]; then
	log "error: no sources were added successfully"
	exit 1
fi

printf 'timestamp,elapsed_s,total,pending,ingesting,failed,stale,completed\n' > "$POLL_FILE"

POLL_TIMED_OUT=0
QUERIES_RAN=0
SEARCH_HTTP_STATUS=0
ASK_HTTP_STATUS=0
SEARCH_RESULT_COUNT=0
ASK_SOURCE_COUNT=0
ASK_ANSWER_LENGTH=0

FINAL_TOTAL=0
FINAL_PENDING=0
FINAL_INGESTING=0
FINAL_FAILED=0
FINAL_STALE=0
FINAL_COMPLETED=0

start_epoch="$(date +%s)"

while true; do
	now_epoch="$(date +%s)"
	elapsed="$((now_epoch - start_epoch))"

	status_file="${RUN_DIR}/sources_status_latest.json"
	status_code="$(api_call "GET" "/v1/collections/${COLLECTION_ID}/sources?page=1&page_size=100&sort=id" "" "$status_file" 1)"

	if [[ "$status_code" != "200" ]]; then
		log "warning: source status poll failed with status=${status_code}; retrying"
		if (( elapsed >= WAIT_TIMEOUT_SEC )); then
			POLL_TIMED_OUT=1
			break
		fi
		sleep "$POLL_INTERVAL_SEC"
		continue
	fi

	FINAL_TOTAL="$(jq '[.sources[]?] | length' "$status_file" 2>/dev/null || echo "0")"
	FINAL_PENDING="$(jq '[.sources[]? | select(.status=="pending")] | length' "$status_file" 2>/dev/null || echo "0")"
	FINAL_INGESTING="$(jq '[.sources[]? | select(.status=="ingesting")] | length' "$status_file" 2>/dev/null || echo "0")"
	FINAL_FAILED="$(jq '[.sources[]? | select(.status=="failed")] | length' "$status_file" 2>/dev/null || echo "0")"
	FINAL_STALE="$(jq '[.sources[]? | select(.status=="stale")] | length' "$status_file" 2>/dev/null || echo "0")"
	FINAL_COMPLETED="$(jq '[.sources[]? | select(.status=="completed")] | length' "$status_file" 2>/dev/null || echo "0")"

	printf '%s,%s,%s,%s,%s,%s,%s,%s\n' "$(date +'%Y-%m-%dT%H:%M:%S%z')" "$elapsed" "$FINAL_TOTAL" "$FINAL_PENDING" "$FINAL_INGESTING" "$FINAL_FAILED" "$FINAL_STALE" "$FINAL_COMPLETED" >> "$POLL_FILE"

	if [[ "$QUERIES_RAN" -eq 0 ]] && (( FINAL_COMPLETED >= MIN_COMPLETED_FOR_QUERY )); then
		log "minimum completed sources reached (${FINAL_COMPLETED}), executing search/ask"
		run_search_and_ask "$COLLECTION_ID"
		QUERIES_RAN=1
	fi

	if (( FINAL_PENDING + FINAL_INGESTING == 0 )); then
		log "source processing reached terminal states"
		break
	fi

	if (( elapsed >= WAIT_TIMEOUT_SEC )); then
		POLL_TIMED_OUT=1
		log "poll timeout reached after ${elapsed}s"
		break
	fi

	sleep "$POLL_INTERVAL_SEC"
done

if [[ "$QUERIES_RAN" -eq 0 ]] && (( FINAL_COMPLETED >= MIN_COMPLETED_FOR_QUERY )); then
	log "running deferred search/ask after polling"
	run_search_and_ask "$COLLECTION_ID"
	QUERIES_RAN=1
fi

RUN_STATUS="failed"
if (( FINAL_COMPLETED >= MIN_COMPLETED_FOR_QUERY )) && [[ "$SEARCH_HTTP_STATUS" == "200" ]] && [[ "$ASK_HTTP_STATUS" == "200" ]]; then
	RUN_STATUS="success"
elif (( FINAL_COMPLETED > 0 )); then
	RUN_STATUS="partial"
fi

jq -n \
	--arg run_id "$RUN_ID" \
	--arg run_status "$RUN_STATUS" \
	--arg api_base_url "$API_BASE_URL" \
	--arg user_email "$USER_EMAIL" \
	--argjson collection_id "$COLLECTION_ID" \
	--argjson source_urls_requested "${#SOURCE_URLS[@]}" \
	--argjson source_create_success "$SOURCE_CREATE_SUCCESS" \
	--argjson source_create_failure "$SOURCE_CREATE_FAILURE" \
	--argjson poll_timed_out "$POLL_TIMED_OUT" \
	--argjson final_total "$FINAL_TOTAL" \
	--argjson final_pending "$FINAL_PENDING" \
	--argjson final_ingesting "$FINAL_INGESTING" \
	--argjson final_failed "$FINAL_FAILED" \
	--argjson final_stale "$FINAL_STALE" \
	--argjson final_completed "$FINAL_COMPLETED" \
	--argjson queries_ran "$QUERIES_RAN" \
	--argjson search_http_status "$SEARCH_HTTP_STATUS" \
	--argjson ask_http_status "$ASK_HTTP_STATUS" \
	--argjson search_result_count "$SEARCH_RESULT_COUNT" \
	--argjson ask_source_count "$ASK_SOURCE_COUNT" \
	--argjson ask_answer_length "$ASK_ANSWER_LENGTH" \
	'{
	  run_id: $run_id,
	  status: $run_status,
	  api_base_url: $api_base_url,
	  user_email: $user_email,
	  collection_id: $collection_id,
	  source_urls_requested: $source_urls_requested,
	  source_create_success: $source_create_success,
	  source_create_failure: $source_create_failure,
	  poll_timed_out: $poll_timed_out,
	  final_source_counts: {
	    total: $final_total,
	    pending: $final_pending,
	    ingesting: $final_ingesting,
	    failed: $final_failed,
	    stale: $final_stale,
	    completed: $final_completed
	  },
	  queries: {
	    ran: ($queries_ran == 1),
	    search_http_status: $search_http_status,
	    search_result_count: $search_result_count,
	    ask_http_status: $ask_http_status,
	    ask_source_count: $ask_source_count,
	    ask_answer_length: $ask_answer_length
	  },
	  artifacts: {
	    log_file: "runner.log",
	    source_create_results_file: "source_create_results.tsv",
	    poll_status_file: "poll_status.csv",
	    search_response_file: "search_response.json",
	    ask_response_file: "ask_response.json"
	  }
	}' > "$SUMMARY_FILE"

log "run summary written: ${SUMMARY_FILE}"
log "run status: ${RUN_STATUS}"
log "artifacts directory: ${RUN_DIR}"

if [[ "$RUN_STATUS" == "failed" ]]; then
	exit 2
fi

exit 0
