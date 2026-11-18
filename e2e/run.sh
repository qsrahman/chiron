#!/bin/sh
# Live end-to-end check: the real binary in a pseudo-terminal, the model of your config, a real stdio MCP server.
# It costs a few model calls and takes several minutes. Usage: e2e/run.sh [-k name]
set -e
cd "$(dirname "$0")"
if [ ! -x .venv/bin/python ]; then
	python3 -m venv .venv
	.venv/bin/pip install --quiet pyte==0.8.2
fi
exec .venv/bin/python live.py "$@"
