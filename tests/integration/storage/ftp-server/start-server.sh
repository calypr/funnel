# This starts a docker container in detached mode, which runs an FTP server.
# This expects to be run in this directory.
# Data and users have been pre-configured.
ls -lhRn _scratch

# Create a FTP password file that matches the host's user/group IDs
cat << EOF > _scratch/passwd/pureftpd.passwd
bob:\$6\$88Q98Coi2ilPLQd0\$obmZnax7kg0zoGEScqsCsKl0MGPH8qUp8zW/QJwXxnNWohl3/2oqCiTAbxdmE.1TmWz4qSN7qDODR6KRp6Wk10:$(id -u):$(id -g)::/home/ftpusers/bob/./::::::::::::
sally:\$6\$C7iB9Y2ozWTJNJK0\$W/b39lnTdghkRB1J/tW1.2g1VqFawj0rVzVM/pLppySdqoJdznmc93ciU8yR7aULs7hnd41XOUaXbKGPOootV1:$(id -u):$(id -g)::/home/ftpusers/sally/./::::::::::::
EOF
docker run --name ftp --rm -d --env FTP_USER_UID=$(id -u) --env FTP_USER_GID=$(id -g) -p 8021:21 -p 30000-30009:30000-30009 -v `pwd`/_scratch/ftp-test/:/home/ftpusers -v `pwd`/_scratch/ftp-var:/var/ftp -v `pwd`/_scratch/passwd:/etc/pure-ftpd/passwd quay.io/ohsu-comp-bio/funnel-ftp-test-server
