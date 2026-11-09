#!/bin/bash
/usr/bin/mysql_install_db
chown -R mysql:mysql /var/lib/mysql
/usr/bin/mysqld_safe --datadir='/var/lib/mysql' &
sleep 10
mysql -NBe "CREATE DATABASE slurm_acct_db"
mysql -NBe "CREATE USER 'slurm'@'localhost'"
mysql -NBe "SET PASSWORD for 'slurm'@'localhost' = password('password')"
mysql -NBe "GRANT USAGE ON *.* to 'slurm'@'localhost'"
mysql -NBe "GRANT ALL PRIVILEGES on slurm_acct_db.* to 'slurm'@'localhost'"
mysql -NBe "FLUSH PRIVILEGES"
killall mysqld
sleep 10
