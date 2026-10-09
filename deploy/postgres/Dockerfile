# postgres:18-alpine is a floating tag: rebuilding with `docker compose build --pull db`
# picks up the latest 18.x minor release and Alpine security patches.
FROM postgres:18-alpine

RUN apk add --no-cache pgbackrest \
    && mkdir -p /var/log/pgbackrest /var/spool/pgbackrest /tmp/pgbackrest \
    && chown -R postgres:postgres /var/log/pgbackrest /var/spool/pgbackrest /tmp/pgbackrest
