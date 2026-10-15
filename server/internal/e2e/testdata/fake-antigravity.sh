#!/bin/sh
# Stands in for agy. The adapter refuses every role run before starting it,
# so this script running at all is a failure the test can see.
echo "agy ran" >&2
exit 3
