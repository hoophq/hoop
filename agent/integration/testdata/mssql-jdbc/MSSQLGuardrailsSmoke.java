import java.sql.Connection;
import java.sql.DatabaseMetaData;
import java.sql.DriverManager;
import java.sql.PreparedStatement;
import java.sql.ResultSet;
import java.sql.SQLException;
import java.sql.Statement;
import java.util.Properties;

public final class MSSQLGuardrailsSmoke {
    private static final String BLOCK_MESSAGE = "blocked by hoop guardrail: secret_table is off limits";

    private MSSQLGuardrailsSmoke() {}

    public static void main(String[] args) throws Exception {
        if (args.length != 5) {
            throw new IllegalArgumentException("usage: MSSQLGuardrailsSmoke <host> <port> <database> <user> <password>");
        }

        String url = "jdbc:sqlserver://" + args[0] + ":" + args[1]
                + ";databaseName=" + args[2]
                + ";encrypt=false"
                + ";loginTimeout=15"
                + ";socketTimeout=30000";
        Properties properties = new Properties();
        properties.setProperty("user", args[3]);
        properties.setProperty("password", args[4]);
        properties.setProperty("applicationName", "Hoop JDBC integration test");

        try (Connection connection = DriverManager.getConnection(url, properties)) {
            if (!connection.isValid(5)) {
                throw new AssertionError("JDBC connection is not valid after login");
            }

            assertScalar(connection, "SELECT 1", 1);
            assertPreparedScalar(connection, 42);
            exerciseMetadata(connection);
            assertBlocked(connection, "SELECT * FROM dbo.secret_table");
            assertBlocked(connection, "EXEC sp_executesql N'SELECT * FROM dbo.sec' + N'ret_table'");
            assertScalar(connection, "SELECT 7", 7);
        }

        System.out.println("JDBC MSSQL guardrail smoke passed");
    }

    private static void assertScalar(Connection connection, String sql, int expected) throws SQLException {
        try (Statement statement = connection.createStatement(); ResultSet rows = statement.executeQuery(sql)) {
            if (!rows.next() || rows.getInt(1) != expected || rows.next()) {
                throw new AssertionError("unexpected scalar result for: " + sql);
            }
        }
    }

    private static void assertPreparedScalar(Connection connection, int expected) throws SQLException {
        try (PreparedStatement statement = connection.prepareStatement("SELECT ?")) {
            statement.setInt(1, expected);
            try (ResultSet rows = statement.executeQuery()) {
                if (!rows.next() || rows.getInt(1) != expected || rows.next()) {
                    throw new AssertionError("unexpected prepared-statement result");
                }
            }
        }
    }

    private static void exerciseMetadata(Connection connection) throws SQLException {
        DatabaseMetaData metadata = connection.getMetaData();
        if (metadata.getDatabaseProductName().isBlank()) {
            throw new AssertionError("JDBC metadata did not report a database product");
        }
        try (ResultSet catalogs = metadata.getCatalogs()) {
            while (catalogs.next()) {
                catalogs.getString(1);
            }
        }
        try (ResultSet schemas = metadata.getSchemas()) {
            while (schemas.next()) {
                schemas.getString(1);
            }
        }
    }

    private static void assertBlocked(Connection connection, String sql) throws SQLException {
        try (Statement statement = connection.createStatement()) {
            statement.execute(sql);
            throw new AssertionError("guardrail did not block: " + sql);
        } catch (SQLException expected) {
            if (expected.getErrorCode() != 50000 || !expected.getMessage().contains(BLOCK_MESSAGE)) {
                throw new AssertionError("unexpected guardrail error for: " + sql, expected);
            }
        }
    }
}
