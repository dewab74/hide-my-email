#!/usr/bin/env bash
# The scripted terminal session that scripts/demo.sh records. Commands are
# "typed" a character at a time, then run.

set -uo pipefail

run() {
  printf '\033[1;32m$\033[0m '
  local i
  for ((i = 0; i < ${#1}; i++)); do
    printf '%s' "${1:i:1}"
    sleep 0.035
  done
  sleep 0.5
  printf '\n'
  eval "$1"
  sleep 1.8
}

printf '\033[H\033[2J'
sleep 0.5
run 'hme login --1password "Apple ID"'
run 'hme generate -d "Acme store"'
run 'hme generate -d "Acme forum" --1password --url acme.example'
run 'hme list'
run 'hme copy grocery'
run 'hme delete dusty-almanac.5p@icloud.com'
run 'hme list --inactive'
sleep 1
