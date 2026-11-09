#!/bin/bash

set -e

# Set the ulimits for this container. 
# Must be run with the --privileged option
# ulimit -l unlimited
# ulimit -s unlimited

sudo /usr/bin/supervisord -c /etc/supervisord.conf

# Run whatever the user wants to
exec "$@"
