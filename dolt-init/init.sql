-- Allow 'dolt' and 'root' users to connect from any host (required for container-to-container access).
-- The root user is restricted to localhost by default in dolthub/dolt-sql-server.
CREATE USER IF NOT EXISTS 'dolt'@'%';
GRANT ALL PRIVILEGES ON *.* TO 'dolt'@'%';
CREATE USER IF NOT EXISTS 'root'@'%';
GRANT ALL PRIVILEGES ON *.* TO 'root'@'%';
FLUSH PRIVILEGES;
