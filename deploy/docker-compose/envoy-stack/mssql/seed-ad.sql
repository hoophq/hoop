-- The Active Directory login: what maps a Kerberos principal onto a SQL
-- identity.
--
-- Kept apart from seed.sql because it is the one step in this stack that does
-- not currently succeed, and mixing it in would either fail the whole bring-up
-- or hide the failure. See mssql/README.md, "What does not work yet".
--
-- FROM WINDOWS marks this as an external principal. The name is NETBIOS form,
-- HOOP\alice, not the Kerberos UPN alice@HOOP.TEST: SQL Server rejects the UPN
-- with "is not a valid Windows NT name. Give the complete name:
-- <domain\username>".
--
-- Creating this login is a DIRECTORY operation, not a Kerberos one. SQL Server
-- resolves the name to a SID over LDAP before it will store the login, which
-- is why this stack runs a Samba domain controller rather than the bare MIT
-- KDC it started with — that KDC issued valid tickets and SQL Server refused
-- every one with error 18452.
SET NOCOUNT ON;
GO

IF NOT EXISTS (SELECT 1 FROM sys.server_principals WHERE name = 'HOOP\alice')
    CREATE LOGIN [HOOP\alice] FROM WINDOWS;
GO

USE appdb;
GO

IF NOT EXISTS (SELECT 1 FROM sys.database_principals WHERE name = 'HOOP\alice')
    CREATE USER [HOOP\alice] FOR LOGIN [HOOP\alice];
GO

ALTER ROLE db_owner ADD MEMBER [HOOP\alice];
GO

SELECT 'ad-login-created' AS status;
GO
