-- Data for the MSSQL lane, plus the SQL login the demo drives the data path
-- with. The Active Directory login lives in seed-ad.sql, which is attempted
-- separately because it depends on SQL Server's directory integration rather
-- than on anything this stack controls.
SET NOCOUNT ON;
GO

IF DB_ID('appdb') IS NULL
    CREATE DATABASE appdb;
GO

USE appdb;
GO

IF OBJECT_ID('dbo.customers', 'U') IS NOT NULL
    DROP TABLE dbo.customers;
GO

CREATE TABLE dbo.customers (
    id    INT IDENTITY(1,1) PRIMARY KEY,
    name  NVARCHAR(100) NOT NULL,
    email NVARCHAR(200) NOT NULL,
    ssn   CHAR(11)      NOT NULL
);
GO

INSERT INTO dbo.customers (name, email, ssn) VALUES
    (N'Ada Lovelace', N'ada@example.com',   '123-45-6789'),
    (N'Grace Hopper', N'grace@example.com', '987-65-4321');
GO

-- A SQL login, so the policy and audit half of the lane can be exercised
-- without depending on the directory. Deliberately given db_owner: the point
-- is that hoop-inspect refuses the DELETE even though the DATABASE would
-- happily allow it. A guardrail that only repeats what the database already
-- enforces proves nothing.
IF NOT EXISTS (SELECT 1 FROM sys.server_principals WHERE name = 'appuser')
    CREATE LOGIN appuser WITH PASSWORD = 'App!Passw0rd', CHECK_POLICY = OFF;
GO

USE appdb;
GO

IF NOT EXISTS (SELECT 1 FROM sys.database_principals WHERE name = 'appuser')
    CREATE USER appuser FOR LOGIN appuser;
GO

ALTER ROLE db_owner ADD MEMBER appuser;
GO

SELECT 'seeded' AS status, COUNT(*) AS customers FROM dbo.customers;
GO
