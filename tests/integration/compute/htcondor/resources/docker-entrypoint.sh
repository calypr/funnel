#!/bin/bash

echo "Starting condor"
sudo /usr/sbin/condor_master

i=0
RUNNING=false
while [ $i -lt 5 ]; do
    condor_q > /dev/null 2>&1
    if [ $? -eq 0 ]; then 
        RUNNING=true
        break
    fi
    sleep 2
    i=$[$i+1]
done

if $RUNNING; then
    echo "condor is running"
    exec "$@"
else
    echo "condor failed to start"
    exit 1
fi
