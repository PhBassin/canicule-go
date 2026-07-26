#!/bin/sh
set -eu

if [ ! -f /data/config.json ]; then
  cp /defaults/config.json /data/config.json
fi
if [ ! -d /data/templates ]; then
  cp -R /defaults/templates /data/templates
fi

exec canicule "$@"
