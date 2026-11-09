SELECT format('CREATE ROLE %I LOGIN PASSWORD %L', :'service_user', :'service_pass')
WHERE NOT EXISTS (
  SELECT 1 FROM pg_roles WHERE rolname = :'service_user'
)\gexec

ALTER ROLE :"service_user" WITH LOGIN PASSWORD :'service_pass';

SELECT format('CREATE DATABASE %I OWNER %I', :'service_db', :'service_user')
WHERE NOT EXISTS (
  SELECT 1 FROM pg_database WHERE datname = :'service_db'
)\gexec

ALTER DATABASE :"service_db" OWNER TO :"service_user";
GRANT ALL ON DATABASE :"service_db" TO :"service_user" WITH GRANT OPTION;
