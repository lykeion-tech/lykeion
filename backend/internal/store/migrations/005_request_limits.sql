CREATE UNLOGGED TABLE request_limits (
 key text NOT NULL, bucket bigint NOT NULL, count integer NOT NULL,
 PRIMARY KEY(key,bucket)
);
