#!/bin/bash

function finish {
    echo "im done here"
}

trap finish EXIT

if [ ! -f "/var/lib/mysql/ibdata1" ]; then
    echo "Initializing database"
    /usr/bin/mysql_install_db
    echo "Database initialized"
    chown -R mysql:mysql /var/lib/mysql
    chown -R mysql:mysql /var/run/mariadb
fi

if [ ! -d "/var/lib/mysql/slurm_acct_db" ]; then
    /usr/bin/mysqld_safe --datadir='/var/lib/mysql' &

    for i in {30..0}; do
        if echo "SELECT 1" | mysql &> /dev/null; then
            break
        fi
        echo "Starting MariaDB"
        sleep 1
    done

    if [ "$i" = 0 ]; then
        echo >&2 "MariaDB did not start"
        exit 1
    fi

    echo "Creating Slurm acct database"
    mysql -NBe "CREATE DATABASE slurm_acct_db"
    mysql -NBe "CREATE USER 'slurm'@'localhost'"
    mysql -NBe "SET PASSWORD for 'slurm'@'localhost' = password('password')"
    mysql -NBe "GRANT USAGE ON *.* to 'slurm'@'localhost'"
    mysql -NBe "GRANT ALL PRIVILEGES on slurm_acct_db.* to 'slurm'@'localhost'"
    mysql -NBe "FLUSH PRIVILEGES"
    echo "Slurm acct database created"
    echo "Stopping MariaDB"
    killall mysqld
    for i in {30..0}; do
        if echo "SELECT 1" | mysql &> /dev/null; then
            sleep 1
        else
            break
        fi
    done
    if [ "$i" = 0 ]; then
        echo >&2 "MariaDB did not stop"
        exit 1
    fi
fi

chown slurm:slurm /var/spool/slurmd /var/run/slurmd /var/lib/slurmd /var/log/slurm

echo "Starting all processes"
/usr/bin/supervisord --configuration /etc/supervisord.conf

i=0
RUNNING=false
while [ $i -lt 5 ]; do
    sinfo > /dev/null 2>&1
    if [ $? -eq 0 ]; then 
        RUNNING=true
        break
    fi
    sleep 2
    i=$[$i+1]
done

if $RUNNING; then
    echo "slurm is running"
    exec "$@"
else
    echo "slurm failed to start"
    exit 1
fi
