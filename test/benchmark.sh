#!/bin/bash
set -euo pipefail

TS=${1:-./test/timeseries.txt}
SERVERS=${2:-10}
CLIENTS=${3:-1000}
DIR=${4:-benchmark}
GOAL=${5:-0}
TSN=$(grep -v '^#' -c "$TS")
echo "Running benchmarking test"

rc=
trap 'rc=$?; printf "cleaning up...\n" && oc delete -f ./manifests/benchmark/ --ignore-not-found=true && oc delete namespace telemeter-benchmark --ignore-not-found=true && jobs -p | xargs -r kill; exit $rc' EXIT

benchmark() {
    local current=$1
    local goal=$2
    local success=0
    while [ "$goal" == 0 ] || [ "$success" -lt "$goal" ]; do
        printf "benchmarking with %d clients sending %d time series\n" "$current" "$TSN"
        create
        client "$current" http://"$(route telemeter-server)" &
        if ! check "$current" https://"$(route benchmark-thanos-query)"; then
            break
        fi
        jobs -p | xargs -r kill
        success=$current
        current=$((current+500))
    done
    printf "Successfully handled %s clients\n" "$success"
    # Only return non-zero if we set a goal and didn't meet it.
    if [ "$goal" -gt 0 ] && [ "$success" -lt "$goal" ]; then
        return 1
    fi
    return 0
}

route() {
    oc get route --namespace=telemeter-benchmark "$1" --output jsonpath='{.spec.host}'
}

create() {
    printf "removing stale resources...\n"
    oc delete -f ./manifests/benchmark/ --ignore-not-found=true > /dev/null && oc delete namespace telemeter-benchmark --ignore-not-found=true > /dev/null
    printf "creating telemeter-server...\n"
    oc create namespace telemeter-benchmark > /dev/null
    # Create everything but the Thanos resources.
    find ./manifests/benchmark/ ! -name '*Thanos*' -type f -print0 | xargs -0l -I{} oc apply -f {} > /dev/null
    oc scale statefulset telemeter-server --namespace telemeter-benchmark --replicas "$SERVERS"
    local retries=20
    until [ "$(oc get pods -n telemeter-benchmark | grep telemeter-server- | grep Running -c)" -eq "$SERVERS" ]; do
        retries=$((retries-1))
        if [ $retries -eq 0 ]; then
            printf "timed out waiting for telemeter-server to be up\n"
            return 1
        fi
        printf "waiting for telemeter-server to be ready; checking again in 10s...\n"
        sleep 10
    done
    printf "creating Thanos...\n"
    # Create everything but the Telemeter server resources as we want
    # to avoid undoing the scaling event.
    find ./manifests/benchmark/ ! -name '*TelemeterServer.yaml' -type f -print0 | xargs -0l -I{} oc apply -f {} > /dev/null
    local retries=20
    until [ "$(oc get pods -n telemeter-benchmark -l 'app.kubernetes.io/part-of=telemeter-benchmark' | grep Running -c)" -eq 5 ]; do
        retries=$((retries-1))
        if [ $retries -eq 0 ]; then
            printf "timed out waiting for Thanos to be up\n"
            return 1
        fi
        printf "waiting for Thanos to be ready; checking again in 10s...\n"
        sleep 10
    done
    printf "successfully created all resources\n"
}

client() {
    trap 'jobs -p | xargs -r kill' EXIT
    local n=$1
    local url=$2
    ./telemeter-benchmark --workers="$n" --metrics-file="$TS" --to="$url" --to-token=benchmark --listen=localhost:8888 > /dev/null 2>&1 &
    wait
}

check() {
    local n=$1
    local url=$2
    local checks=60
    while query "$url" ; do
        printf "\tno scrape failures; "
        checks=$((checks-1))
        if [ $checks -eq 0 ]; then
            printf "successfully completed run!\n"
            printf "PASS: telemeter-server handled %d clients\n" "$n"
            save "$n" "$url"
            return 0
        fi
        printf "checking again in 15s...\n"
        sleep 15
    done
    printf "FAIL: telemeter-server failed check\n"
    save "$n" "$url"
    return 1
}

query() {
    local url=$1
    local res
    res=$(curl --fail --silent -G -k "$url"/api/v1/query --data-urlencode 'query=sum_over_time(count(up{job=~"telemeter-server.*"} == 0)[2w:])')
    echo "$res" | jq --exit-status '.data.result | length == 0' > /dev/null
}

save() {
    local n=$1
    local url=$2
    local res
    mkdir -p "$DIR"
    res=$(curl --fail --silent -G -k "$url"/api/v1/query_range --data-urlencode "query=(irate(process_cpu_seconds_total[1m]) * 100)" --data-urlencode "start=$(date -d '1 hour ago' +%s)" --data-urlencode "end=$(date +%s)" --data-urlencode "step=1")
    echo "$res" > "$DIR"/"$SERVERS"_"$n"_cpu.json
    res=$(curl --fail --silent -G -k "$url"/api/v1/query_range --data-urlencode "query=process_resident_memory_bytes" --data-urlencode "start=$(date -d '1 hour ago' +%s)" --data-urlencode "end=$(date +%s)" --data-urlencode "step=1")
    echo "$res" > "$DIR"/"$SERVERS"_"$n"_mem.json
}

benchmark "$CLIENTS" "$GOAL"

exit $?
