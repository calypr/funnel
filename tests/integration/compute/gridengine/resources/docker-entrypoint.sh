#!/bin/bash

set -e

source /usr/share/gridengine/default/common/settings.sh

echo "$HOSTNAME" >  /usr/share/gridengine/default/common/act_qmaster
echo "domain $HOSTNAME" >> /etc/resolv.conf
/etc/init.d/sgemaster start
/etc/init.d/sge_execd start

qconf -as $HOSTNAME
qconf -mattr "hostgroup" "hostlist" "$HOSTNAME" "@allhosts"
qconf -dattr "hostgroup" "hostlist" "docker" "@allhosts"
qconf -mattr "queue" "hostlist" "$HOSTNAME" "debug"
qconf -dattr "queue" "hostlist" "docker" "debug"
qconf -mattr "queue" "slots" "8" "all.q"

# Run whatever the user wants to
exec "$@"
