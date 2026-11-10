-- Runs once, the first time the Postgres container initialises.
-- The default database (orderops_auth, for the auth service) is created from
-- POSTGRES_DB; the orders service gets its own database here — one database
-- per service.
CREATE DATABASE orderops_orders;
