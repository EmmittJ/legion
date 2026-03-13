-- Initialize the Beads database
-- Dolt SQL server starts with root@127.0.0.1 with authentication enabled.
-- We need to allow remote connections and ensure root user works properly.

CREATE DATABASE IF NOT EXISTS beads;

-- Drop the original localhost-only root user and create a new one for remote access
DROP USER IF EXISTS 'root'@'127.0.0.1';
CREATE USER IF NOT EXISTS 'root'@'%' IDENTIFIED WITH 'mysql_native_password' BY '';

-- Also ensure dolt user exists
CREATE USER IF NOT EXISTS 'dolt'@'%' IDENTIFIED WITH 'mysql_native_password' BY '';

-- Grant full privileges
GRANT ALL PRIVILEGES ON *.* TO 'root'@'%' WITH GRANT OPTION;
GRANT ALL PRIVILEGES ON *.* TO 'dolt'@'%' WITH GRANT OPTION;

FLUSH PRIVILEGES;
